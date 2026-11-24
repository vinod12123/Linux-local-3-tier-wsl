package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const dockerAPIVersion = "v1.41"

var allowedServices = map[string]serviceDefinition{
	"frontend": {container: "astra-lab-frontend", crashCommand: []string{"sh", "-c", "sleep 1; kill -SEGV 1"}},
	"backend":  {container: "astra-lab-backend", backendCrash: true},
	"database": {container: "astra-lab-database", crashCommand: []string{"sh", "-c", "sleep 1; kill -ABRT 1"}},
}

type serviceDefinition struct {
	container    string
	crashCommand []string
	backendCrash bool
}

type controller struct {
	docker       *http.Client
	controlToken string
	backendURL   string
}

type containerState struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Status  string `json:"status"`
	Health  string `json:"health"`
	Exit    int    `json:"exitCode"`
}

func main() {
	docker := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
			},
		},
		Timeout: 10 * time.Second,
	}
	app := &controller{
		docker:       docker,
		controlToken: requiredEnv("LAB_CONTROL_TOKEN"),
		backendURL:   envOr("LAB_BACKEND_URL", "http://backend:8080"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveControlPage)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "up"}) })
	mux.HandleFunc("GET /api/status", app.status)
	mux.HandleFunc("POST /api/services/{service}/{action}", app.action)

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8082"),
		Handler:           securityHeaders(requestLogger(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("event=controller_started address=%s allowed_containers=astra-lab-frontend,astra-lab-backend,astra-lab-database", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("event=controller_failed error=%q", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func (c *controller) status(w http.ResponseWriter, _ *http.Request) {
	states := make(map[string]containerState, len(allowedServices))
	for name, service := range allowedServices {
		state, err := c.inspect(service.container)
		if err != nil {
			states[name] = containerState{Name: service.container, Status: "unavailable"}
			continue
		}
		states[name] = state
	}
	writeJSON(w, http.StatusOK, states)
}

func (c *controller) action(w http.ResponseWriter, r *http.Request) {
	serviceName := r.PathValue("service")
	action := r.PathValue("action")
	service, allowed := allowedServices[serviceName]
	if !allowed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown lab service"})
		return
	}
	var err error
	switch action {
	case "stop":
		err = c.dockerPost("/containers/"+url.PathEscape(service.container)+"/stop?t=10", nil)
	case "restore":
		err = c.dockerPost("/containers/"+url.PathEscape(service.container)+"/start", nil)
	case "crash":
		if service.backendCrash {
			err = c.crashBackend()
		} else {
			err = c.exec(service.container, service.crashCommand)
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "action must be stop, crash, or restore"})
		return
	}
	if err != nil {
		log.Printf("level=error event=simulation_failed service=%s action=%s error=%q", serviceName, action, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "service": serviceName, "action": action})
		return
	}
	log.Printf("level=warning event=simulation_triggered service=%s action=%s", serviceName, action)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "service": serviceName, "action": action})
}

func (c *controller) inspect(container string) (containerState, error) {
	resp, err := c.dockerRequest(http.MethodGet, "/containers/"+url.PathEscape(container)+"/json", nil)
	if err != nil {
		return containerState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return containerState{}, dockerError(resp)
	}
	var payload struct {
		Name  string `json:"Name"`
		State struct {
			Running  bool   `json:"Running"`
			Status   string `json:"Status"`
			ExitCode int    `json:"ExitCode"`
			Health   *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return containerState{}, err
	}
	health := "not-configured"
	if payload.State.Health != nil {
		health = payload.State.Health.Status
	}
	return containerState{
		Name: strings.TrimPrefix(payload.Name, "/"), Running: payload.State.Running,
		Status: payload.State.Status, Health: health, Exit: payload.State.ExitCode,
	}, nil
}

func (c *controller) crashBackend() error {
	req, err := http.NewRequest(http.MethodPost, c.backendURL+"/_lab/crash", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Lab-Control-Token", c.controlToken)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("backend refused crash request: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *controller) exec(container string, command []string) error {
	payload, _ := json.Marshal(map[string]any{
		"AttachStdout": false, "AttachStderr": false, "Tty": false, "Cmd": command,
	})
	resp, err := c.dockerRequest(http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return dockerError(resp)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return err
	}
	if created.ID == "" {
		return fmt.Errorf("Docker returned no exec ID")
	}
	return c.dockerPost("/exec/"+url.PathEscape(created.ID)+"/start", []byte(`{"Detach":true,"Tty":false}`))
}

func (c *controller) dockerPost(path string, body []byte) error {
	resp, err := c.dockerRequest(http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dockerError(resp)
	}
	return nil
}

func (c *controller) dockerRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, "http://docker/"+dockerAPIVersion+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.docker.Do(req)
}

func dockerError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("Docker API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func serveControlPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, controlPage)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("event=control_request method=%s path=%s duration_ms=%d", r.Method, r.URL.Path, time.Since(started).Milliseconds())
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("required environment variable %s is missing", name)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

const controlPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Payment Lab Recovery Console</title><style>
body{font:16px system-ui;background:#0b1220;color:#e5e7eb;margin:0}.wrap{max-width:960px;margin:auto;padding:32px}h1{margin-bottom:6px}.note{color:#94a3b8}.warn{background:#422006;border:1px solid #a16207;padding:14px;border-radius:10px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px;margin-top:22px}.card{background:#111827;border:1px solid #334155;border-radius:12px;padding:18px}.state{font-weight:700;margin:12px 0}.up{color:#4ade80}.down{color:#f87171}button{padding:9px 12px;margin:4px;border:0;border-radius:7px;cursor:pointer}.crash{background:#dc2626;color:white}.stop{background:#d97706;color:white}.restore{background:#16a34a;color:white}a{color:#60a5fa}pre{white-space:pre-wrap}</style></head>
<body><main class="wrap"><h1>Payment Lab Recovery Console</h1><p class="note">This page remains available on port 18082 even when the frontend is down.</p>
<p class="warn"><strong>Local lab only.</strong> Stop is an intentional Docker stop. Crash terminates the service process abnormally. Restore starts the selected container without deleting its data.</p>
<p><a href="http://localhost:18080">Open payment application</a></p><section id="services" class="grid"></section><pre id="message"></pre></main>
<script>
const names=['frontend','backend','database'];
async function refresh(){try{const r=await fetch('/api/status');const data=await r.json();document.querySelector('#services').innerHTML=names.map(n=>{const s=data[n]||{};const up=s.running;return '<article class="card"><h2>'+n+'</h2><div class="state '+(up?'up':'down')+'">'+(s.status||'unknown')+' · '+(s.health||'unknown')+' · exit '+(s.exitCode??'-')+'</div><button class="crash" onclick="act(\''+n+'\',\'crash\')">Crash</button><button class="stop" onclick="act(\''+n+'\',\'stop\')">Stop</button><button class="restore" onclick="act(\''+n+'\',\'restore\')">Restore</button></article>'}).join('')}catch(e){msg(e.message)}}
async function act(service,action){if(!confirm(action+' '+service+'?'))return;try{const r=await fetch('/api/services/'+service+'/'+action,{method:'POST'});const body=await r.json();msg(JSON.stringify(body,null,2));setTimeout(refresh,1200)}catch(e){msg(e.message)}}
function msg(v){document.querySelector('#message').textContent=v}refresh();setInterval(refresh,5000);
</script></body></html>`
