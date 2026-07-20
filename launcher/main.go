// Forge Solo — Windows launcher/orchestrator (tray app, no console). Boots a bundled Postgres,
// the BCH2 + 1175 nodes, and the stratum + api services, serves the dashboard on 127.0.0.1, and
// opens the browser. All data + secrets live under %APPDATA%\ForgeSolo. Only the stratum miner
// port (3333) is bound on 0.0.0.0; everything else is 127.0.0.1 only.
package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
)

// BCH2 logo, shown as the system-tray icon (Windows accepts .ico bytes).
// The same .ico is compiled into the exe's resources (rsrc.syso) for the
// taskbar/shortcut icon, and set as the installer icon.
//
//go:embed forge-solo.ico
var trayIcon []byte

// Fixed, user-facing / internet-facing ports (matched by the installer's firewall rules).
const (
	minerPort = "3333" // documented miner endpoint — fixed so users always point miners here
	webPort   = "3080" // dashboard URL — fixed so it stays stable across launches
	bch2P2P   = "8333" // BCH2 P2P (incoming peers) — fixed so the installer firewall rule matches
)

// Loopback-only service ports. Chosen dynamically at startup (pickPort) so they can NEVER
// collide with other software or Windows reserved/excluded ranges — the root cause of the
// api-on-8080 (Apache/XAMPP) and 1175-RPC-on-25361 (WSAEACCES 10013) bind failures. Assigned
// in main() before anything binds; every conf/env/proxy reads these vars, and writeAlways
// regenerates the configs each launch, so a run is internally consistent.
var (
	pgPort     = "54329"
	bch2RPC    = "8332"
	bch2ZMQ    = "28332"
	aux1175RPC = "25361"
	aux1175P2P = "25360"
	stratumInt = "3337"
	apiPort    = "8080"
)

var (
	installDir string
	dataDir    string
	mu         sync.Mutex
	procs      = map[string]*exec.Cmd{}
	sec        secrets
)

type secrets struct {
	BCH2Pass, AuxPass, DBPass, Token string
}

func gen() string    { b := make([]byte, 24); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func md(p string)    { _ = os.MkdirAll(p, 0o755) }
func dpath(e ...string) string { return filepath.Join(append([]string{dataDir}, e...)...) }
func ipath(e ...string) string { return filepath.Join(append([]string{installDir}, e...)...) }

// belowNormal = BELOW_NORMAL_PRIORITY_CLASS. Run the heavy background processes
// (both nodes + Postgres) below normal so IBD can't starve the desktop on a laptop.
// A child process inherits its parent's priority class on Windows.
const belowNormal = 0x00004000

func hiddenPrio(extraFlags uint32, name string, args ...string) *exec.Cmd {
	c := exec.Command(ipath(name), args...)
	c.Dir = installDir
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | extraFlags} // CREATE_NO_WINDOW
	return c
}
func hidden(name string, args ...string) *exec.Cmd { return hiddenPrio(0, name, args...) }
func run(key string, c *exec.Cmd) error {
	if err := c.Start(); err != nil {
		return err
	}
	mu.Lock()
	procs[key] = c
	mu.Unlock()
	return nil
}
func stop(key string) {
	mu.Lock()
	if c := procs[key]; c != nil && c.Process != nil {
		_ = c.Process.Kill()
		delete(procs, key)
	}
	mu.Unlock()
}
func writeAbsent(path, content string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte(content), 0o600)
	}
}

// writeAlways rewrites generated config every boot so upgrades pick up new settings.
// Safe here: these files are machine-generated (the user's payout address lives in the DB).
func writeAlways(path, content string) { _ = os.WriteFile(path, []byte(content), 0o600) }
func waitTCP(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}
func openBrowser(u string) { _ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start() }

// pickPort returns the first free loopback TCP port at/above start. Scanning a fixed low
// range (not :0) keeps the port out of the OS ephemeral pool, so it won't be reused for an
// outbound socket between selection and bind; a reserved or in-use port simply fails to bind
// and we move on to the next.
func pickPort(start int) string {
	for p := start; p < start+300; p++ {
		if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			_ = l.Close()
			return strconv.Itoa(p)
		}
	}
	return strconv.Itoa(start)
}

func main() {
	exe, _ := os.Executable()
	installDir = filepath.Dir(exe)
	dataDir = filepath.Join(os.Getenv("APPDATA"), "ForgeSolo")
	md(dataDir)
	// Assign collision-proof loopback ports before any service binds. Distinct 300-wide
	// windows keep the services from picking the same port as each other.
	pgPort = pickPort(30000)
	bch2RPC = pickPort(30300)
	bch2ZMQ = pickPort(30600)
	aux1175RPC = pickPort(30900)
	aux1175P2P = pickPort(31200)
	stratumInt = pickPort(31500)
	apiPort = pickPort(31800)
	systray.Run(onReady, func() { shutdown() })
}

func onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("Forge Solo")
	systray.SetTooltip("Forge Solo — starting…")
	mOpen := systray.AddMenuItem("Open Dashboard", "")
	mRestart := systray.AddMenuItem("Restart Mining", "Restart the miner after changing your payout address")
	mData := systray.AddMenuItem("Open Data Folder", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit Forge Solo", "")
	go boot()
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openBrowser("http://127.0.0.1:" + webPort)
			case <-mRestart.ClickedCh:
				restartMiner()
			case <-mData.ClickedCh:
				_ = exec.Command("explorer", dataDir).Start()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}
