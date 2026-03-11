package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	log "github.com/rs/zerolog/log"
)

func secHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func runWebServer(ctx context.Context, listen string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", handleAPIConfig)
	mux.HandleFunc("/api/status", handleAPIStatus)
	mux.HandleFunc("/api/meters", handleAPIMeters)
	mux.HandleFunc("/api/reconnect", handleAPIReconnect)
	mux.HandleFunc("/api/network", handleAPINetwork)
	mux.HandleFunc("/api/proxy", handleAPIProxy)
	mux.HandleFunc("/api/contexts", handleAPIContexts)
	mux.HandleFunc("/api/logs", handleAPILogs)
	mux.HandleFunc("/api/connections", handleAPIConnections)
	mux.HandleFunc("/api/downloads", handleAPIDownloads)
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	srv := &http.Server{
		Addr:              listen,
		Handler:           secHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		MaxHeaderBytes:    1 << 18, // 256 KB
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info().Str("addr", listen).Msg("Web UI listening")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error().Err(err).Msg("Web server error")
	}
}

func handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(cfg)

	case http.MethodPost:
		// Pre-populate with current values so omitted fields stay unchanged.
		incoming := cfg
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16) // 64 KB
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if _, err := zerolog.ParseLevel(incoming.LogLevel); err != nil {
			http.Error(w, "invalid log level: "+incoming.LogLevel, http.StatusBadRequest)
			return
		}

		if incoming.Listen != cfg.Listen {
			http.Error(w, "changing the hamlib listen address requires restarting Flexy", http.StatusUnprocessableEntity)
			return
		}

		cfg = incoming

		// Apply log level immediately without reconnect.
		if level, err := zerolog.ParseLevel(cfg.LogLevel); err == nil {
			zerolog.SetGlobalLevel(level)
		}

		// Signal a reconnect to pick up radio-related changes.
		select {
		case reconnectCh <- struct{}{}:
		default:
		}

		json.NewEncoder(w).Encode(cfg)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, errMsg := getConnState()
	resp := map[string]interface{}{
		"state":       state.String(),
		"error":       errMsg,
		"localIP":     getLocalIP(),
		"meterPktsRx": meterPktsRx.Load(),
	}

	if state == ConnStateConnected && fc != nil {
		if slice, ok := fc.GetObject("slice " + SliceIdx); ok {
			resp["frequency"] = slice["RF_frequency"]
			if translated, ok := modesFromFlex[slice["mode"]]; ok {
				resp["mode"] = translated
			} else {
				resp["mode"] = slice["mode"]
			}
		}
		if interlock, ok := fc.GetObject("interlock"); ok {
			resp["ptt"] = interlock["state"] == "TRANSMITTING"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleAPIMeters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	meterMu.RLock()
	defer meterMu.RUnlock()

	out := make(map[string]float64, len(hamlibToFlex))
	for hamlibName, flexName := range hamlibToFlex {
		if v, ok := meterVal[flexName]; ok {
			conv := meters[hamlibName]
			if conv != nil {
				v = conv(v)
			}
			out[hamlibName] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func handleAPIReconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	select {
	case reconnectCh <- struct{}{}:
	default:
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}` + "\n"))
}

func handleAPINetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type addrInfo struct {
		CIDR      string `json:"cidr"`
		IP        string `json:"ip"`
		Tailscale bool   `json:"tailscale,omitempty"`
	}
	type ifaceInfo struct {
		Name  string     `json:"name"`
		Addrs []addrInfo `json:"addrs"`
		Bcast string     `json:"broadcast,omitempty"`
	}

	ifaces, _ := net.Interfaces()
	ifaceList := make([]ifaceInfo, 0)
	var allIPs []addrInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		info := ifaceInfo{Name: iface.Name}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}
			ai := addrInfo{
				CIDR:      ipNet.String(),
				IP:        ipNet.IP.String(),
				Tailscale: isTailscaleIP(ipNet.IP),
			}
			info.Addrs = append(info.Addrs, ai)
			allIPs = append(allIPs, ai)
			if ip4 := ipNet.IP.To4(); ip4 != nil {
				mask := ipNet.Mask
				if len(mask) == 4 {
					bcast := make(net.IP, 4)
					for i := range bcast {
						bcast[i] = ip4[i] | ^mask[i]
					}
					info.Bcast = bcast.String()
				}
			}
		}
		if len(info.Addrs) > 0 {
			ifaceList = append(ifaceList, info)
		}
	}

	var kv map[string]string
	var bcast string
	var unicastAddrs []string
	if rc := getRadioContext(); rc != nil {
		rc.mu.RLock()
		kv = rc.DiscoveryKV
		bcast = rc.BroadcastAddrs
		unicastAddrs = rc.UnicastAddrs
		rc.mu.RUnlock()
	}

	resp := map[string]interface{}{
		"interfaces":     ifaceList,
		"allIPs":         allIPs,
		"localIPToRadio": getLocalIP(),
		"radioAddr":      cfg.RadioIP,
		"bindings": map[string]string{
			"hamlib": cfg.Listen,
			"web":    cfg.WebListen,
			"proxy":  cfg.ProxyListen,
		},
		"radioBindIP": cfg.RadioBindIP,
		"discovery": map[string]interface{}{
			"proxyIP":       cfg.ProxyIP,
			"broadcastAddr": bcast,
			"unicastAddrs":  unicastAddrs,
			"kv":            kv,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleAPIProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if rc := getRadioContext(); rc != nil {
		json.NewEncoder(w).Encode(rc.Snapshot())
	} else {
		w.Write([]byte("null\n"))
	}
}

func handleAPIContexts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var contexts []RadioContextSnapshot
	if rc := getRadioContext(); rc != nil {
		contexts = append(contexts, rc.Snapshot())
	}
	json.NewEncoder(w).Encode(contexts)
}

func handleAPILogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		since, _ = strconv.ParseInt(s, 10, 64)
	}
	lines, lastSeq := logBuf.Since(since)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"lines":   lines,
		"lastSeq": lastSeq,
	})
}

func handleAPIConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, errMsg := getConnState()

	radioConn := map[string]interface{}{
		"addr":         cfg.RadioIP,
		"state":        state.String(),
		"error":        errMsg,
		"station":      cfg.Station,
		"sliceLetter":  cfg.Slice,
		"sliceIdx":     SliceIdx,
		"clientHandle": ClientID,
		"localIP":      getLocalIP(),
	}
	if state == ConnStateConnected && fc != nil {
		if slice, ok := fc.GetObject("slice " + SliceIdx); ok {
			radioConn["frequency"] = slice["RF_frequency"]
			if translated, ok := modesFromFlex[slice["mode"]]; ok {
				radioConn["mode"] = translated
			} else {
				radioConn["mode"] = slice["mode"]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"radioConn":   radioConn,
		"hamlibConns": snapshotHamlibClients(),
	}
	if rc := getRadioContext(); rc != nil {
		resp["proxyContext"] = rc.Snapshot()
	}
	json.NewEncoder(w).Encode(resp)
}

func handleAPIDownloads(w http.ResponseWriter, r *http.Request) {
	type dlEntry struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var files []dlEntry
	fs.WalkDir(webFS, "downloads", func(path string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr
		}
		files = append(files, dlEntry{Name: d.Name(), Size: info.Size()})
		return nil
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}
