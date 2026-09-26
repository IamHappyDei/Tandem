//go:build windows

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"tandem/internal/ui"
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	pIsAdmin = shell32.NewProc("IsUserAnAdmin")
	pCSIDL   = shell32.NewProc("SHGetFolderPathW")
	pSendMsg = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageW")
)

const (
	appName             = "Tandem"
	setupTitle          = "Tandem setup"
	exeName             = "tandem.exe"
	setupName           = "tandem-setup.exe"
	regKey              = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Tandem`
	regKeyUser          = `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Tandem`
	runKeyMachine       = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	runKeyUser          = `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	csidlCommonPrograms = 0x0017
)

var payload = []string{exeName, "tandem.ico", "GUIDE.md", "README.md", "allow-in-firewall.bat"}

var leftovers = []string{"FOR-YOUR-FRIEND.txt", "gsxsync.exe", "gsx-sync.exe", "uninstall.exe", "tandem.exe.old"}

type rep interface {
	step(name string)
	ok(text string)
	bad(text string)
	run(text string)
	pct(n int)
}

type stdoutRep struct{ n, total int }

func (s *stdoutRep) step(name string) { fmt.Printf("%s\n", name) }
func (s *stdoutRep) ok(t string)      { fmt.Printf("  ok   %s\n", t) }
func (s *stdoutRep) bad(t string)     { fmt.Printf("  !    %s\n", t) }
func (s *stdoutRep) run(t string)     { fmt.Printf("  ..   %s\n", t) }
func (s *stdoutRep) pct(int)          {}

func main() {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	uninstall := fs.Bool("uninstall", false, "remove Tandem and its firewall rule")
	purge := fs.Bool("purge", false, "with --uninstall, also delete %APPDATA%\\Tandem")
	quiet := fs.Bool("quiet", false, "no window, no pause - for scripts")
	dir := fs.String("dir", "", "install folder (default Program Files\\Tandem)")
	port := fs.Int("ui", 0, "internal: port the setup window should talk to")
	noElevate := fs.Bool("no-elevate", false, "internal: this copy already asked for administrator rights")
	_ = fs.Parse(normalizeArgs(os.Args[1:]))

	sink = os.Stdout
	if f, err := os.OpenFile(filepath.Join(os.TempDir(), "tandem-setup.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		sink = io.MultiWriter(os.Stdout, f)
		fmt.Fprintf(f, "\n--- %s setup pid=%d elevated=%v args=%v\n", time.Now().Format(time.RFC3339), os.Getpid(), elevated(), os.Args[1:])
	}

	graphical := !*quiet && !*uninstall && *dir == ""

	switch {
	case *uninstall:
		r := &stdoutRep{}
		r.step("removing Tandem")
		if admin, err := runUninstall(true, *purge, r); err != nil || admin {
			if err != nil {
				fmt.Fprintln(sink, "uninstall failed:", err)
			}
			if admin {
				fmt.Fprintln(sink, "an every-account copy is still there - run this file as administrator to finish")
			}
			os.Exit(1)
			fmt.Fprintln(sink, "uninstall failed:", err)
			os.Exit(1)
		}
		return
	case graphical:
		if !elevated() && !*noElevate && relaunchWith("--no-elevate") {
			return
		}
		if err := runGUI(*port); err != nil {
			fmt.Fprintln(sink, "cannot show the setup window:", err)
			os.Exit(1)
		}
		return
	}

	t := target{filepath.Join(programFiles(), appName), true}
	if *dir != "" {
		t = target{*dir, strings.EqualFold(filepath.Dir(*dir), programFiles())}
	}
	r := &stdoutRep{}
	r.step(fmt.Sprintf("Tandem %s installer - %s", ui.Version, t.dir))
	if err := runInstall(&t, true, true, r); err != nil {
		fmt.Fprintln(sink, "install failed:", err)
		os.Exit(1)
	}
	if !*quiet {
		fmt.Fprintln(sink, "\ninstalled. Tandem is starting.")
		go func() { _ = exec.Command(t.exe()).Start() }()
		fmt.Print("press Enter to close the installer: ")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

func targetOf() target {
	t := target{filepath.Join(programFiles(), appName), true}
	if !hasFile(t.exe()) {
		if _, mine := targets(); hasFile(mine.exe()) {
			t = mine
		}
	}
	return t
}

func runInstall(t *target, wantFirewall, wantAutorun bool, r rep) error {
	if !hasPayload() && !hasFile(filepath.Join(srcDir(), exeName)) {
		return fmt.Errorf("%s", payloadState())
	}
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", t.dir, err)
	}
	r.pct(4)
	r.step("writing files")
	names := payloadNames()
	done := 0
	for _, name := range names {
		p := filepath.Join(t.dir, name)
		if f, size, err := embedded(name); err == nil {
			err = installBytes(name, p, f, size)
			f.Close()
			if err != nil {
				return fmt.Errorf("install %s: %w", name, err)
			}
		} else if from := filepath.Join(srcDir(), name); hasFile(from) {
			if err := copyFile(from, p); err != nil {
				return fmt.Errorf("copy %s: %w", name, err)
			}
		} else {
			r.run(name + " is not in this build, skipped")
			continue
		}
		r.ok(fmt.Sprintf("%-22s %s", name, sha12(p)))
		done++
		r.pct(4 + done*34/len(names))
	}
	if done == 0 {
		return fmt.Errorf("nothing was written")
	}

	r.step("start menu")
	if err := shortcut(t.exe(), t.dir, t.machine); err != nil {
		r.bad("start menu: " + err.Error())
	} else {
		r.ok("type " + appName + " in the taskbar to open it")
	}
	r.pct(52)

	if wantFirewall {
		r.step("windows firewall")
		if err := firewall(t.exe()); err != nil {
			r.bad(err.Error())
		} else {
			r.ok("inbound allowed for the installed copy")
		}
	} else {
		r.ok("firewall left as it was - others cannot connect in until it is allowed")
	}
	r.pct(66)

	r.step("autorun")
	if err := autorun(t.exe(), wantAutorun, t.machine); err != nil {
		r.bad(err.Error())
	} else if wantAutorun {
		r.ok("starts when you sign in")
	} else {
		r.ok("only when you open it")
	}
	r.pct(78)

	r.step("add/remove programs")
	if err := uninstallEntry(t.dir, t.machine); err != nil {
		r.bad(err.Error())
	} else {
		r.ok("listed as " + appName)
	}
	r.pct(92)
	return nil
}

func runUninstall(wantFirewall, purge bool, r rep) (needsAdmin bool, err error) {
	all, mine := targets()
	self, _ := os.Executable()
	here := filepath.Dir(self)
	places := []target{}
	for _, t := range []target{all, mine} {

		if hasFile(t.exe()) {
			places = append(places, t)
		}
	}
	if len(places) == 0 {
		r.step("nothing to remove")
		r.ok("no Tandem in either usual folder - the rule and the registry entries are cleared anyway")
	}

	r.pct(10)
	if wantFirewall {
		r.step("windows firewall")
		if err := firewallDelete(); err != nil {
			r.bad(err.Error())
		} else {
			r.ok("rule removed")
		}
	}
	r.pct(25)

	r.step("start menu and autorun")
	removeShortcuts()
	for _, machine := range []bool{true, false} {
		_ = exec.Command("reg", "delete", runKeyOf(machine), "/v", appName, "/f").Run()
	}
	r.ok("cleared in both the per-account and the every-account places")
	r.pct(40)

	r.step("files")
	_ = exec.Command("taskkill", "/F", "/IM", exeName).Run()
	time.Sleep(400 * time.Millisecond)
	for _, t := range places {
		var gone, stuck []string
		for _, name := range append(append(payload, setupName), leftovers...) {
			p := filepath.Join(t.dir, name)
			if !hasFile(p) {
				continue
			}
			if err := os.Remove(p); err != nil {
				stuck = append(stuck, name)
			} else {
				gone = append(gone, name)
			}
		}
		if len(gone) > 0 {
			r.ok(fmt.Sprintf("%d file(s) out of %s", len(gone), t.dir))
		}
		if len(stuck) > 0 {
			if !sameDir(t.dir, here) {
				needsAdmin = true
			}
			r.bad(fmt.Sprintf("%d left in %s: %s", len(stuck), t.dir, whyStuck(stuck, here, t.dir)))
		}
		if sameDir(t.dir, here) {
			selfRemoveLater(t.dir)
		}
		if dirEmpty(t.dir) {
			_ = os.Remove(t.dir)
		}
	}
	r.pct(75)

	r.step("add/remove programs")
	for _, key := range []string{regKey, regKeyUser} {
		out, err := exec.Command("reg", "delete", key, "/f").CombinedOutput()
		switch {
		case err == nil:
			r.ok("removed from " + key)
		case strings.Contains(strings.ToLower(string(out)), "unable to find"):
		case strings.Contains(string(out), "elevation") || strings.Contains(string(out), "Access is denied"):
			r.bad(key + " is still there - it belongs to the every-account install and needs administrator rights")
		default:
			r.bad(key + ": " + firstLine(string(out)))
		}
	}
	r.pct(90)

	if purge {
		_ = os.RemoveAll(filepath.Join(envOr("APPDATA", ""), appName))
		r.ok("settings deleted")
	} else {
		r.ok("settings kept in " + filepath.Join(envOr("APPDATA", ""), appName))
	}
	r.pct(100)
	return needsAdmin, nil
}

func runKeyOf(machine bool) string {
	if machine {
		return runKeyMachine
	}
	return runKeyUser
}
func elevated() bool {
	r, _, _ := pIsAdmin.Call()
	return r != 0
}

func relaunch(alreadyElevated bool) bool {
	if alreadyElevated {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		fmt.Sprintf("Start-Process -FilePath '%s' -Verb RunAs", self))
	return cmd.Run() == nil
}

func flagArgs() []string {
	out := []string{}
	for _, a := range os.Args[1:] {
		if !strings.HasPrefix(a, "-ui") && !strings.HasPrefix(a, "/ui") {
			out = append(out, a)
		}
	}
	return out
}

func normalizeArgs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {

		if len(a) > 1 && a[0] == '/' && !strings.ContainsAny(a[1:], "/:\\") {
			a = "--" + a[1:]
		}
		out = append(out, a)
	}
	return out
}

func shortcut(exe, workdir string, machine bool) error {
	all, err := programsDir(csidlCommonPrograms)
	if err == nil {
		if e := makeLink(all, exe, workdir); e == nil {
			return nil
		}
	}
	mine := filepath.Join(envOr("APPDATA", ""), "Microsoft", "Windows", "Start Menu", "Programs")
	if machine && strings.EqualFold(mine, all) {
		return fmt.Errorf("cannot write a shortcut into %s", all)
	}
	return makeLink(mine, exe, workdir)
}

func makeLink(dir, exe, workdir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lnk := filepath.Join(dir, appName+".lnk")
	ps := fmt.Sprintf(
		"$ErrorActionPreference='Stop';$w=New-Object -ComObject WScript.Shell;$s=$w.CreateShortcut('%s');"+
			"$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.IconLocation='%s,0';$s.Description='%s - shared cockpit ground';$s.Save()",
		lnk, exe, workdir, exe, appName)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", ps).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", firstLine(string(out)))
	}
	return nil
}

func removeShortcuts() {
	if dir, err := programsDir(csidlCommonPrograms); err == nil {
		_ = os.Remove(filepath.Join(dir, appName+".lnk"))
	}
	mine := filepath.Join(envOr("APPDATA", ""), "Microsoft", "Windows", "Start Menu", "Programs")
	_ = os.Remove(filepath.Join(mine, appName+".lnk"))
}

func programsDir(csidl uint) (string, error) {
	buf := make([]uint16, syscall.MAX_PATH)
	r, _, err := pCSIDL.Call(0, uintptr(csidl|0x4000), 0, 0, uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return "", fmt.Errorf("shgetfolderpath: %v", err)
	}
	return syscall.UTF16ToString(buf), nil
}

func firewall(exe string) error {
	_, _ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+appName).CombinedOutput()
	out, err := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+appName, "dir=in", "action=allow", "program="+exe, "enable=yes", "profile=any").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", firstLine(string(out)))
	}
	return nil
}

func firewallDelete() error {
	out, err := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+appName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", firstLine(string(out)))
	}
	return nil
}

func autorun(exe string, on bool, machine bool) error {
	key := runKeyUser
	if machine && elevated() {
		key = runKeyMachine
	}
	if !on {
		_ = exec.Command("reg", "delete", key, "/v", appName, "/f").Run()
		return nil
	}
	out, err := exec.Command("reg", "add", key, "/v", appName, "/t", "REG_SZ", "/d",
		`"`+exe+`" --minimized`, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", firstLine(string(out)))
	}
	return nil
}

func uninstallEntry(into string, machine bool) error {
	key := regKeyUser
	if machine {
		key = regKey
	}
	vals := [][3]string{
		{"DisplayName", "REG_SZ", appName},
		{"DisplayVersion", "REG_SZ", ui.Version},
		{"Publisher", "REG_SZ", "Tandem"},
		{"InstallDate", "REG_SZ", time.Now().Format("20060102")},
		{"InstallLocation", "REG_SZ", into},
		{"DisplayIcon", "REG_SZ", filepath.Join(into, exeName)},
		{"UninstallString", "REG_SZ", `"` + filepath.Join(into, setupName) + `" --uninstall`},
		{"QuietUninstallString", "REG_SZ", `"` + filepath.Join(into, setupName) + `" --uninstall --quiet`},
		{"NoRepair", "REG_DWORD", "1"},
		{"WindowsInstaller", "REG_DWORD", "0"},
		{"EstimatedSize", "REG_DWORD", itoa(sizeOf(into) / 1024)},
	}
	for _, v := range vals {
		out, err := exec.Command("reg", "add", key, "/v", v[0], "/t", v[1], "/d", v[2], "/f").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s", v[0], firstLine(string(out)))
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".new"
	outf, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(outf, in); err != nil {
		_ = outf.Close()
		_ = os.Remove(tmp)
		return err
	}
	_ = outf.Close()
	if hasFile(to) {
		_ = os.Remove(to)
	}
	return os.Rename(tmp, to)
}

func sha12(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:12]
}

func sizeOf(dir string) int64 {
	var n int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

func dirEmpty(p string) bool {
	fs, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	return len(fs) == 0
}

func firstLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", "\n"))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func hasFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func programFiles() string { return envOr("ProgramFiles", `C:\Program Files`) }

func srcDir() string {
	self, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(self)
}

func envOr(k, fall string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	if k == "LOCALAPPDATA" || k == "APPDATA" {
		if h, err := os.UserHomeDir(); err == nil {
			if k == "LOCALAPPDATA" {
				return filepath.Join(h, "AppData", "Local")
			}
			return filepath.Join(h, "AppData", "Roaming")
		}
	}
	return fall
}

func itoa(n int64) string { return fmt.Sprintf("%d", n) }

var sink io.Writer = os.Stdout

func sameDir(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return strings.EqualFold(strings.TrimRight(aa, `\`), strings.TrimRight(bb, `\`))
}

func whyStuck(names []string, here, dir string) string {
	if sameDir(dir, here) {
		return "that is this setup, it removes itself in a moment"
	}
	if len(names) == 1 && names[0] == setupName {
		return "the setup file is in use; delete it once this window has closed"
	}
	return "needs administrator rights, or Tandem is still running from there"
}

func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

func selfRemoveLater(dir string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	script := filepath.Join(os.TempDir(), fmt.Sprintf("tandem-sweep-%d.ps1", os.Getpid()))
	body := fmt.Sprintf(`$own = '%[3]s'
$exe = '%[1]s'
$dir = '%[2]s'
for ($i = 0; $i -lt 20; $i++) {
  if (Test-Path -LiteralPath $exe) { Remove-Item -Force -LiteralPath $exe -ErrorAction SilentlyContinue }
  if (Test-Path -LiteralPath $dir) {
    if (-not (Get-ChildItem -Force -LiteralPath $dir)) { Remove-Item -Recurse -Force -LiteralPath $dir -ErrorAction SilentlyContinue }
  }
  if (-not (Test-Path -LiteralPath $exe) -and -not (Test-Path -LiteralPath $dir)) { break }
  Start-Sleep -Milliseconds 500
}
Remove-Item -Force -LiteralPath $own -ErrorAction SilentlyContinue
`, psQuote(self), psQuote(dir), psQuote(script))
	if os.WriteFile(script, []byte(body), 0o644) != nil {
		return
	}

	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		"Start-Process -WindowStyle Hidden -FilePath powershell.exe -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','"+psQuote(script)+"'")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	_ = cmd.Start()
	_ = cmd.Wait()
}

func relaunchWith(args ...string) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	list := ""
	for _, a := range args {
		list += "'" + strings.ReplaceAll(a, "'", "''") + "',"
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs", psQuote(self), strings.TrimRight(list, ",")))
	return cmd.Run() == nil
}
