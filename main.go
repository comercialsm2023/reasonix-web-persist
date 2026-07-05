// reasonix-web-persist — reverse proxy for reasonix serve that persists web UI
// settings to ~/.reasonix/config.toml on-the-fly. No modifications to Reasonix.
//
// Usage:
//   1. Stop reasonix serve
//   2. Edit systemd unit: change port to 8788 (or any port)
//      ExecStart=/usr/bin/reasonix serve -addr 127.0.0.1:8788
//   3. systemctl daemon-reload && systemctl start reasonix
//   4. Run: reasonix-web-persist
//      (listens on 8787, proxies to 8788)
//
// Or replace the systemd ExecStart directly with:
//   /usr/local/bin/reasonix-web-persist -upstream 127.0.0.1:8788 -listen :8787
// and start reasonix serve separately (or have this exec it).

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/ini.v1"
)

func main() {
	listen := getEnv("LISTEN", ":8787")
	upstream := getEnv("UPSTREAM", "http://127.0.0.1:8788")
	configPath := getEnv("CONFIG_PATH", defaultConfigPath())
	manageReasonix := os.Getenv("MANAGE_REASONIX") == "1"

	proxy := &persistProxy{
		upstream:   upstream,
		configPath: configPath,
	}

	// If MANAGE_REASONIX=1, start reasonix serve as a child process
	if manageReasonix {
		upstreamPort := mustExtractPort(upstream)
		cmd := exec.Command("reasonix", "serve", "-addr", "127.0.0.1:"+upstreamPort)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Fatalf("failed to start reasonix: %v", err)
		}
		defer cmd.Process.Kill()
		log.Printf("started reasonix serve on 127.0.0.1:%s", upstreamPort)
	}

	srv := &http.Server{
		Addr:    listen,
		Handler: proxy,
	}

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		srv.Close()
	}()

	log.Printf("reasonix-web-persist listening on %s → %s", listen, upstream)
	log.Printf("config path: %s", configPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

// persistProxy reverse-proxies to reasonix serve and intercepts settings changes.
type persistProxy struct {
	upstream   string
	configPath string
}

func (p *persistProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Read the full body so we can inspect it
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Check if this is a settings-changing request
	p.maybePersist(r.Method, r.URL.Path, body)

	// Forward to upstream
	u, _ := url.Parse(p.upstream)
	u.Path = r.URL.Path
	u.RawQuery = r.URL.RawQuery

	proxyReq, err := http.NewRequest(r.Method, u.String(), io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		http.Error(w, "proxy create", http.StatusInternalServerError)
		return
	}
	proxyReq.Header = r.Header.Clone()

	resp, err := http.DefaultTransport.RoundTrip(proxyReq)
	if err != nil {
		http.Error(w, "proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (p *persistProxy) maybePersist(method, path string, body []byte) {
	if method != "POST" {
		return
	}

	switch {
	case path == "/tool-approval-mode":
		var req struct {
			Mode string `json:"mode"`
		}
		if json.Unmarshal(body, &req) != nil || req.Mode == "" {
			return
		}
		log.Printf("persisting tool_approval_mode=%s", req.Mode)
		p.setConfig("default_tool_approval_mode", req.Mode)

	case path == "/auto-approve-tools":
		var req struct {
			On bool `json:"on"`
		}
		if json.Unmarshal(body, &req) != nil {
			return
		}
		val := "ask"
		if req.On {
			val = "yolo"
		}
		log.Printf("persisting tool_approval_mode=%s (from auto-approve)", val)
		p.setConfig("default_tool_approval_mode", val)

	case path == "/submit":
		var req struct {
			Input string `json:"input"`
		}
		if json.Unmarshal(body, &req) != nil {
			return
		}
		trimmed := strings.TrimSpace(req.Input)

		if strings.HasPrefix(trimmed, "/model ") {
			ref := strings.TrimSpace(strings.TrimPrefix(trimmed, "/model"))
			if ref != "" {
				log.Printf("persisting default_model=%s", ref)
				p.setConfig("default_model", ref)
			}
		}
	}
}

// setConfig writes a key-value pair to ~/.reasonix/config.toml.
func (p *persistProxy) setConfig(key, value string) {
	cfg, err := ini.Load(p.configPath)
	if err != nil {
		// If file doesn't exist, create a minimal one
		cfg = ini.Empty()
	}

	// Ensure the root section is created if needed
	// We write to the default section
	cfg.Section("").Key(key).SetValue(value)

	var buf bytes.Buffer
	if _, err := cfg.WriteTo(&buf); err != nil {
		log.Printf("error serializing config: %v", err)
		return
	}

	// Preserve the [[providers]] array sections by reading and rewriting
	// the file more carefully — ini library may corrupt array sections.
	// Simple approach: read existing, add/modify the flat key at top.
	raw, err := os.ReadFile(p.configPath)
	if err != nil {
		// File doesn't exist, write from scratch
		os.WriteFile(p.configPath, buf.Bytes(), 0644)
		return
	}

	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
			lines[i] = key + ` = "` + value + `"`
			found = true
			break
		}
	}
	if !found {
		// Insert before the first `[[providers]]` or append at end
		insertAt := len(lines)
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "[[providers]]") {
				insertAt = i
				break
			}
		}
		newLines := make([]string, 0, len(lines)+1)
		newLines = append(newLines, lines[:insertAt]...)
		newLines = append(newLines, key+` = "`+value+`"`)
		newLines = append(newLines, lines[insertAt:]...)
		lines = newLines
	}

	result := strings.Join(lines, "\n")
	if err := os.WriteFile(p.configPath, []byte(result), 0644); err != nil {
		log.Printf("error writing config: %v", err)
	}
}

func mustExtractPort(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Fatalf("invalid upstream URL %q: %v", rawURL, err)
	}
	_, port, _ := net.SplitHostPort(u.Host)
	return port
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".reasonix", "config.toml")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
