using System;
using System.Collections.Generic;
using System.Globalization;
using System.Net.WebSockets;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.FlightSimulator.SimConnect;

namespace TandemSim
{
    [StructLayout(LayoutKind.Sequential)]
    struct Datum
    {
        public double value;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct TextDatum
    {
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)]
        public string text;
    }

    static class Program
    {
        const uint USER = 0;
        const string AppName = "Tandem";

        static SimConnect sim;
        static readonly object gate = new object();
        static readonly List<Watched> plan = new List<Watched>();
        static readonly Dictionary<uint, string> bound = new Dictionary<uint, string>();
        static JsonElement wantedJson;
        static ClientWebSocket sock;
        static bool connectedToSim;
        static int intervalMs = 250;
        static string url = "ws://127.0.0.1:8796/";
        static uint nextTextDef = 9000;

        sealed class Watched
        {
            public string Name;
            public string Unit;
            public bool IsText;
            public uint Def;
            public uint Req;
            public double Last = double.NaN;
            public string LastText;
        }

        static int Main(string[] argv)
        {
            for (int i = 0; i < argv.Length; i++)
            {
                if (argv[i] == "--port" && i + 1 < argv.Length)
                {
                    url = "ws://127.0.0.1:" + argv[++i] + "/";
                }
                else if (argv[i] == "--interval" && i + 1 < argv.Length)
                {
                    intervalMs = int.Parse(argv[++i], CultureInfo.InvariantCulture);
                }
            }
            Log("tandem-sim is asking the sim what the aircraft's switches say, and telling " + url);
            var pump = new Thread(() => { Pump().Wait(); });
            pump.IsBackground = true;
            pump.Start();
            while (true)
            {
                try
                {
                    EnsureSim();
                    PumpSim();
                }
                catch (Exception e)
                {
                    if (connectedToSim) { Log("the sim link ended: " + e.Message); }
                    connectedToSim = false;
                    sim = null;
                    lock (gate) { plan.Clear(); bound.Clear(); }
                    Thread.Sleep(3000);
                }
                Thread.Sleep(intervalMs);
            }
        }

        static void Log(string s)
        {
            Console.WriteLine(DateTime.Now.ToString("HH:mm:ss ") + s);
        }

        static void EnsureSim()
        {
            if (sim != null) { return; }
            if (sock == null || sock.State != WebSocketState.Open) { return; }
            try
            {
                sim = new SimConnect(AppName, IntPtr.Zero, 0, null, 0);
            }
            catch (Exception e)
            {
                Log("the sim is not running, or does not accept a second client (" + e.Message + ")");
                return;
            }
            sim.OnRecvOpen += (SimConnect s, SIMCONNECT_RECV_OPEN e) =>
            {
                connectedToSim = true;
                Log("the sim answered");
            };
            sim.OnRecvQuit += (SimConnect s, SIMCONNECT_RECV e) =>
            {
                Log("the sim closed the door");
                connectedToSim = false;
            };
            sim.OnRecvException += (SimConnect s, SIMCONNECT_RECV_EXCEPTION e) =>
                Log("the sim refused that request (" + e.dwException + ")");
            sim.OnRecvSimobjectData += OnData;
            Log("the sim is reachable");
        }

        static void PumpSim()
        {
            if (sim == null) { return; }
            if (dispatch == null)
            {
                simSignal = new SignalProcDelegate(Signal);
                dispatch = new Thread(() =>
                {
                    try { sim.ReceiveDispatch(simSignal); }
                    catch (Exception e) { Log("the sim link ended (" + e.Message + ")"); connectedToSim = false; }
                });
                dispatch.IsBackground = true;
                dispatch.Start();
            }
            Reconcile();
        }

        static Thread dispatch;
        static SignalProcDelegate simSignal;

        static void Signal(SIMCONNECT_RECV pData, uint cbData)
        {
            var d = pData as SIMCONNECT_RECV_SIMOBJECT_DATA;
            if (d != null) { OnData(null, (SIMCONNECT_RECV_SIMOBJECT_DATA)d); }
        }

        static void Reconcile()
        {
            var want = ParseWanted();
            lock (gate)
            {
                bool same = want.Count == plan.Count;
                if (same)
                {
                    for (int i = 0; i < want.Count; i++)
                    {
                        if (want[i].Name != plan[i].Name || want[i].Unit != plan[i].Unit) { same = false; break; }
                    }
                }
                if (same) { return; }
                plan.Clear();
                bound.Clear();
            }
            uint def = 1, req = 100;
            AddIdentity(ref def, ref req);
            foreach (var w in want)
            {
                w.Def = def;
                w.Req = req;
                try
                {
                    if (w.IsText)
                    {
                        sim.AddToDataDefinition((SimDef)w.Def, w.Name, w.Unit,
                            SIMCONNECT_DATATYPE.STRING256, 0f, 0);
                    }
                    else
                    {
                        sim.AddToDataDefinition((SimDef)w.Def, w.Name, w.Unit,
                            SIMCONNECT_DATATYPE.FLOAT64, 0f, 0);
                    }
                    sim.RequestDataOnSimObject((SimReq)w.Req, (SimDef)w.Def, USER,
                        SIMCONNECT_PERIOD.VISUAL_FRAME, SIMCONNECT_DATA_REQUEST_FLAG.CHANGED, 0, 0, 0);
                    lock (gate)
                    {
                        plan.Add(w);
                        bound[w.Def] = w.Name;
                    }
                    def++;
                    req++;
                }
                catch (Exception e)
                {
                    Log("the sim would not hand over " + w.Name + " (" + e.Message + ")");
                }
            }
            lock (gate) { Log("it is reporting " + plan.Count + " of this aircraft's switches"); }
        }

        enum SimDef { First = 1 }
        enum SimReq { First = 100 }

        static void AddIdentity(ref uint def, ref uint req)
        {
            foreach (var pair in new[] { Tuple.Create("aircraft.title", "TITLE"), Tuple.Create("aircraft.tail", "TAIL NUMBER") })
            {
                var w = new Watched { Name = pair.Item2, Unit = "string", IsText = true, Def = def, Req = req };
                try
                {
                    sim.AddToDataDefinition((SimDef)w.Def, w.Name, w.Unit, SIMCONNECT_DATATYPE.STRING256, 0f, 0);
                    sim.RequestDataOnSimObject((SimReq)w.Req, (SimDef)w.Def, USER,
                        SIMCONNECT_PERIOD.VISUAL_FRAME, SIMCONNECT_DATA_REQUEST_FLAG.CHANGED, 0, 0, 0);
                    lock (gate)
                    {
                        plan.Add(w);
                        bound[w.Def] = pair.Item1;
                    }
                    def++;
                    req++;
                }
                catch (Exception e) { Log("could not ask who we are: " + e.Message); }
            }
        }

        static void OnData(SimConnect sender, SIMCONNECT_RECV_SIMOBJECT_DATA e)
        {
            string name;
            object payload = (e.dwData != null && e.dwData.Length > 0) ? e.dwData[0] : null;
            lock (gate)
            {
                if (!bound.TryGetValue(e.dwDefineID, out name)) { return; }
            }
            if (name.StartsWith("aircraft."))
            {
                var td = ToText(payload);
                if (!string.IsNullOrEmpty(td))
                {
                    Send("{\"type\":\"patch\",\"path\":\"" + name + "\",\"value\":\"" + Esc(td) + "\"}");
                }
                return;
            }
            double v = ToNumber(payload);
            if (double.IsNaN(v)) { return; }
            lock (gate)
            {
                foreach (var w in plan)
                {
                    if (w.Name == name)
                    {
                        if (!double.IsNaN(w.Last) && w.Last == v) { return; }
                        w.Last = v;
                        break;
                    }
                }
            }
            Send("{\"type\":\"patch\",\"path\":\"vars." + Esc(name) + "\",\"value\":" +
                v.ToString("R", CultureInfo.InvariantCulture) + "}");
        }

        static double ToNumber(object payload)
        {
            if (payload == null) { return double.NaN; }
            if (payload is IConvertible)
            {
                try { return Convert.ToDouble(payload, CultureInfo.InvariantCulture); }
                catch { return double.NaN; }
            }
            return double.NaN;
        }

        static string ToText(object payload)
        {
            return payload == null ? null : payload.ToString();
        }

        static List<Watched> ParseWanted()
        {
            var outp = new List<Watched>();
            JsonElement raw;
            lock (gate) { raw = wantedJson; }
            if (raw.ValueKind != JsonValueKind.Array) { return outp; }
            foreach (var item in raw.EnumerateArray())
            {
                if (item.ValueKind != JsonValueKind.String) { continue; }
                var w = Parse(item.GetString());
                if (w != null) { outp.Add(w); }
            }
            return outp;
        }

        static Watched Parse(string s)
        {
            s = (s ?? "").Trim();
            if (s.Length == 0) { return null; }
            int comma = s.IndexOf(',');
            var w = new Watched();
            if (comma < 0)
            {
                w.Name = s;
                w.Unit = s.StartsWith("L:") || s.StartsWith("B:") || s.StartsWith("R:") ? "Bool" : "Float64";
            }
            else
            {
                w.Name = s.Substring(0, comma).Trim();
                w.Unit = s.Substring(comma + 1).Trim();
            }
            w.IsText = w.Unit.Equals("string", StringComparison.OrdinalIgnoreCase);
            return w;
        }

        static void WriteVar(string name, double v)
        {
            if (sim == null) { return; }
            uint def = nextTextDef++;
            try
            {
                var d = Parse(name);
                sim.AddToDataDefinition((SimDef)def, d.Name, d.Unit, SIMCONNECT_DATATYPE.FLOAT64, 0f, 0);
                sim.SetDataOnSimObject((SimDef)def, USER, 0, new Datum { value = v });
            }
            catch (Exception e)
            {
                Log("could not put " + name + " where a coworker left it (" + e.Message + ")");
            }
        }

        static async Task Pump()
        {
            var buf = new byte[128 * 1024];
            while (true)
            {
                if (sock == null || sock.State != WebSocketState.Open)
                {
                    try
                    {
                        sock = new ClientWebSocket();
                        await sock.ConnectAsync(new Uri(url), CancellationToken.None);
                        Send("{\"type\":\"subscribe\",\"channels\":[\"state\"]}");
                    }
                    catch { sock = null; await Task.Delay(2000); continue; }
                }
                try
                {
                    var res = await sock.ReceiveAsync(new ArraySegment<byte>(buf), CancellationToken.None);
                    if (res.MessageType == WebSocketMessageType.Close) { sock = null; continue; }
                    Handle(Encoding.UTF8.GetString(buf, 0, res.Count));
                }
                catch
                {
                    lock (gate) { wantedJson = default; }
                    sock = null;
                    await Task.Delay(1000);
                }
            }
        }

        static void Handle(string text)
        {
            JsonElement doc;
            try { doc = JsonDocument.Parse(text).RootElement.Clone(); } catch { return; }
            string type = Str(doc, "type");
            if (type == "snapshot")
            {
                if (doc.TryGetProperty("wanted", out var w)) { lock (gate) { wantedJson = w.Clone(); } }
                return;
            }
            if (type != "patch") { return; }
            var path = Str(doc, "path");
            if (!path.StartsWith("vars.")) { return; }
            double v = 0;
            if (doc.TryGetProperty("value", out var val))
            {
                if (val.ValueKind == JsonValueKind.True) { v = 1; }
                else if (val.ValueKind == JsonValueKind.False) { v = 0; }
                else if (!double.TryParse(val.ToString(), NumberStyles.Any, CultureInfo.InvariantCulture, out v)) { v = 0; }
            }
            WriteVar(path.Substring(5), v);
        }

        static string Str(JsonElement e, string k)
        {
            if (e.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.String) { return v.GetString(); }
            return "";
        }

        static string Esc(string s)
        {
            var b = new StringBuilder();
            foreach (char c in s)
            {
                if (c == '"' || c == '\\') { b.Append('!'); }
                else if (c < 32) { b.Append(' '); }
                else { b.Append(c); }
            }
            return b.ToString();
        }

        static void Send(string body)
        {
            var s = sock;
            if (s == null || s.State != WebSocketState.Open) { return; }
            try
            {
                s.SendAsync(new ArraySegment<byte>(Encoding.UTF8.GetBytes(body)),
                    WebSocketMessageType.Text, true, CancellationToken.None).Wait(500);
            }
            catch { }
        }
    }
}
