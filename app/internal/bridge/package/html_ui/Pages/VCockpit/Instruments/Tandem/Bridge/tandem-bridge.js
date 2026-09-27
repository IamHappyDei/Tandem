class TandemBridgeElement extends HTMLElement {
  connectedCallback() {
    if (this.__tandem) return;
    this.__tandem = true;
    this.port = 8796;
    this.wanted = [];
    this.timer = null;
    this.last = {};
    this.connect();
  }

  disconnectedCallback() {
    this.close();
  }

  close() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    if (this.sock) {
      try { this.sock.close(); } catch (e) {}
    }
    this.sock = null;
  }

  connect() {
    this.sock = new WebSocket("ws://127.0.0.1:" + this.port + "/");
    this.sock.onopen = () => {
      this.send({ type: "subscribe", channels: ["state"] });
      this.identify();
      this.poll();
      this.label("Tandem: linked");
    };
    this.sock.onclose = () => {
      this.timer && clearInterval(this.timer);
      this.timer = null;
      this.label("Tandem: retrying");
      setTimeout(() => this.connect(), 3000);
    };
    this.sock.onerror = () => {};
    this.sock.onmessage = (ev) => this.onFrame(ev);
  }

  label(text) {
    const el = document.getElementById("tandem-state");
    if (el) el.textContent = text;
  }

  onFrame(ev) {
    let f;
    try { f = JSON.parse(ev.data); } catch (e) { return; }
    if (f.type === "snapshot") {
      this.wanted = Object.keys(f.wanted || {});
      return;
    }
    if (f.type === "patch" && f.write) {
      const name = (f.path || "").replace(/^vars\./, "");
      if (!name) return;
      try {
        SimVar.SetSimVarValue(name, "Float64", Number(f.value));
      } catch (e) {
        console.log("Tandem could not write " + name + ": " + e);
      }
    }
  }

  identify() {
    let title = "";
    try {
      title = SimVar.GetSimVarValue("TITLE", "string") || "";
    } catch (e) {}
    let tail = "";
    try {
      tail = SimVar.GetSimVarValue("TAIL NUMBER", "string") || "";
    } catch (e) {}
    this.send({ type: "patch", path: "aircraft.title", value: title });
    this.send({ type: "patch", path: "aircraft.tail", value: tail });
  }

  poll() {
    if (this.timer) clearInterval(this.timer);
    this.timer = setInterval(() => this.tick(), 500);
  }

  tick() {
    if (!this.wanted.length) return;
    const changed = {};
    for (const name of this.wanted) {
      let v;
      try {
        v = SimVar.GetSimVarValue(name, "Float64");
      } catch (e) {
        continue;
      }
      if (typeof v === "number" && !isFinite(v)) continue;
      if (this.last[name] !== v) {
        this.last[name] = v;
        changed["vars." + name] = v;
      }
    }
    const keys = Object.keys(changed);
    if (!keys.length) return;
    for (const k of keys) {
      this.send({ type: "patch", path: k, value: changed[k] });
    }
  }

  send(obj) {
    if (this.sock && this.sock.readyState === 1) {
      this.sock.send(JSON.stringify(obj));
    }
  }
}
customElements.define("tandem-bridge", TandemBridgeElement);
