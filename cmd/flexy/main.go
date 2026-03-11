package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kc2g-flex-tools/flexclient"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	log "github.com/rs/zerolog/log"

	"github.com/radiolabme/flexy/internal/config"
)

const reconnectDelay = 5 * time.Second

type Config struct {
	RadioIP          string
	Station          string
	Slice            string
	Headless         bool
	SliceCreateParms string
	Listen           string
	WebListen        string
	ProxyListen      string
	ProxyIP          string
	RadioBindIP      string
	Profile          string
	LogLevel         string
	ChkVFOMode       string
	Metering         bool
	LogPings         bool
	ProxyOnly        bool
	UDPPort          int
}

var cfg Config

func init() {
	flag.StringVar(&cfg.RadioIP, "radio", ":discover:", "radio IP address or discovery spec")
	flag.IntVar(&cfg.UDPPort, "udp-port", 0, "udp port to listen for VITA packets (0: random free port)")
	flag.StringVar(&cfg.Station, "station", "Flex", "station name to bind to or create")
	flag.StringVar(&cfg.Slice, "slice", "A", "slice letter to control")
	flag.BoolVar(&cfg.Headless, "headless", false, "run in headless mode")
	flag.StringVar(&cfg.Listen, "listen", ":4532", "hamlib listen [address]:port")
	flag.StringVar(&cfg.WebListen, "web", "", "web UI listen [address]:port (disabled if empty)")
	flag.StringVar(&cfg.ProxyListen, "proxy", "", "SmartSDR proxy listen [address]:port (disabled if empty)")
	flag.StringVar(&cfg.ProxyIP, "proxy-ip", "", "IP to advertise in discovery (auto-detected if empty)")
	flag.StringVar(&cfg.RadioBindIP, "radio-bind-ip", "", "local IP to bind when connecting to radio (for Tailscale/VPN; auto if empty)")
	flag.StringVar(&cfg.Profile, "profile", "", "global profile to load on startup for -headless mode")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "minimum level of messages to log to console")
	flag.StringVar(&cfg.ChkVFOMode, "chkvfo-mode", "new", "chkvfo syntax (old,new)")
	flag.BoolVar(&cfg.Metering, "metering", true, "support reading meters from radio")
	flag.BoolVar(&cfg.LogPings, "log-pings", false, "include ping/pong lines in proxy debug logs")
	flag.BoolVar(&cfg.ProxyOnly, "proxy-only", false, "run only the proxy/web UI; skip flexclient radio connection")

	flag.Bool("setup", false, "run interactive setup wizard")
}

// applyConfigFile applies values from the XDG config file for any CLI flags
// that were not explicitly set on the command line.
func applyConfigFile(c *config.Config) {
	set := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if !set["radio"] && c.Radio != "" {
		cfg.RadioIP = c.Radio
	}
	if !set["station"] && c.Station != "" {
		cfg.Station = c.Station
	}
	if !set["slice"] && c.Slice != "" {
		cfg.Slice = c.Slice
	}
	if !set["listen"] && c.Listen != "" {
		cfg.Listen = c.Listen
	}
	if !set["web"] && c.Web != "" {
		cfg.WebListen = c.Web
	}
	if !set["proxy"] && c.Proxy != "" {
		cfg.ProxyListen = c.Proxy
	}
	if !set["proxy-ip"] && c.ProxyIP != "" {
		cfg.ProxyIP = c.ProxyIP
	}
	if !set["radio-bind-ip"] && c.RadioBindIP != "" {
		cfg.RadioBindIP = c.RadioBindIP
	}
	if !set["profile"] && c.Profile != "" {
		cfg.Profile = c.Profile
	}
	if !set["log-level"] && c.LogLevel != "" {
		cfg.LogLevel = c.LogLevel
	}
	if !set["chkvfo-mode"] && c.ChkVFOMode != "" {
		cfg.ChkVFOMode = c.ChkVFOMode
	}
	if !set["metering"] && c.Metering != nil {
		cfg.Metering = *c.Metering
	}
	if !set["headless"] && c.Headless {
		cfg.Headless = true
	}
	if !set["log-pings"] && c.LogPings {
		cfg.LogPings = true
	}
	if !set["proxy-only"] && c.ProxyOnly {
		cfg.ProxyOnly = true
	}
	if !set["udp-port"] && c.UDPPort != 0 {
		cfg.UDPPort = c.UDPPort
	}
}

var fc *flexclient.FlexClient
var hamlib *HamlibServer = NewHamlibServer()
var ClientID string
var ClientUUID string
var SliceIdx string

// reconnectCh is signaled by the web UI to trigger a graceful reconnect.
var reconnectCh = make(chan struct{}, 1)

func createClient(ctx context.Context) error {
	log.Info().Str("ctx", "flexy").Str("proto", "TCP").Msg("Registering client")
	ClientID = "0x" + fc.ClientID()

	if _, err := fc.SendAndWaitContext(ctx, "client program Hamlib-Flex"); err != nil {
		return err
	}
	if _, err := fc.SendAndWaitContext(ctx, "client station "+strings.ReplaceAll(cfg.Station, " ", "\x7f")); err != nil {
		return err
	}

	log.Info().Str("ctx", "flexy").Str("proto", "TCP").Str("handle", ClientID).Msg("Got client handle")

	if cfg.Profile != "" {
		res, err := fc.SendAndWaitContext(ctx, "profile global load "+cfg.Profile)
		if err != nil {
			return err
		}
		if res.Error != 0 {
			log.Warn().Str("ctx", "flexy").Msgf("Profile load failed: %08X (typo?)", res.Error)
		} else {
			log.Info().Str("ctx", "flexy").Str("profile", cfg.Profile).Msg("Loaded profile")
		}
	}
	return nil
}

func bindClient(ctx context.Context) error {
	log.Info().Str("station", cfg.Station).Msg("Waiting for station")

	clients := make(chan flexclient.StateUpdate)
	sub := fc.Subscribe(flexclient.Subscription{Prefix: "client ", Updates: clients})
	cmdResult := fc.SendNotify("sub client all")

	var found, cmdComplete bool

	for !found || !cmdComplete {
		select {
		case <-ctx.Done():
			cmdResult.Close()
			fc.Unsubscribe(sub)
			return ctx.Err()
		case upd := <-clients:
			if upd.CurrentState["station"] == cfg.Station {
				ClientID = strings.TrimPrefix(upd.Object, "client ")
				ClientUUID = upd.CurrentState["client_id"]
				found = true
			}
		case <-cmdResult.C:
			cmdComplete = true
		}
	}
	cmdResult.Close()

	fc.Unsubscribe(sub)

	log.Info().Str("client_id", ClientID).Str("uuid", ClientUUID).Msg("Found client")

	if _, err := fc.SendAndWaitContext(ctx, "client bind client_id="+ClientUUID); err != nil {
		return err
	}
	return nil
}

func findSlice(ctx context.Context) error {
	log.Info().Str("ctx", "flexy").Str("slice_id", cfg.Slice).Msg("Looking for slice")
	slices := make(chan flexclient.StateUpdate)
	sub := fc.Subscribe(flexclient.Subscription{Prefix: "slice ", Updates: slices})
	cmdResult := fc.SendNotify("sub slice all")

	var found, cmdComplete bool

	for !found || !cmdComplete {
		select {
		case <-ctx.Done():
			cmdResult.Close()
			fc.Unsubscribe(sub)
			return ctx.Err()
		case upd := <-slices:
			if upd.CurrentState["index_letter"] == cfg.Slice {
				SliceIdx = strings.TrimPrefix(upd.Object, "slice ")
				found = true
			}
		case <-cmdResult.C:
			cmdComplete = true
		}
	}
	cmdResult.Close()
	fc.Unsubscribe(sub)
	log.Info().Str("ctx", "flexy").Str("slice_idx", SliceIdx).Msg("Found slice")
	return nil
}

// runRadio manages one complete radio connection lifecycle. It blocks until
// ctx is cancelled (reconnect or shutdown) or the radio disconnects.
func runRadio(ctx context.Context) error {
	setConnState(ConnStateConnecting, nil)

	// NewFlexClient blocks forever during discovery when no radio is reachable.
	// Run it in a goroutine so ctx cancellation can interrupt us.
	type connResult struct {
		fc  *flexclient.FlexClient
		err error
	}
	ch := make(chan connResult, 1)
	go func() {
		client, err := flexclient.NewFlexClient(cfg.RadioIP)
		ch <- connResult{client, err}
	}()

	select {
	case <-ctx.Done():
		setConnState(ConnStateDisconnected, nil)
		return ctx.Err()
	case res := <-ch:
		if res.err != nil {
			setConnState(ConnStateError, res.err)
			return res.err
		}
		fc = res.fc
	}

	var err error

	// runCtx is cancelled when fc.Run() exits or when ctx is cancelled.
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		fc.Run()
		log.Info().Msg("FlexClient exited")
		runCancel()
	}()

	if cfg.Headless {
		if err = createClient(runCtx); err != nil {
			fc.Close()
			wg.Wait()
			if ctx.Err() == nil {
				setConnState(ConnStateError, err)
			}
			return err
		}
	} else {
		if err = bindClient(runCtx); err != nil {
			fc.Close()
			wg.Wait()
			if ctx.Err() == nil {
				setConnState(ConnStateError, err)
			}
			return err
		}
	}

	if err = findSlice(runCtx); err != nil {
		fc.Close()
		wg.Wait()
		if ctx.Err() == nil {
			setConnState(ConnStateError, err)
		}
		return err
	}

	if _, err = fc.SendAndWaitContext(runCtx, "sub radio all"); err != nil {
		log.Error().Err(err).Msg("Failed to subscribe to radio updates")
	}
	if _, err = fc.SendAndWaitContext(runCtx, "sub tx all"); err != nil {
		log.Error().Err(err).Msg("Failed to subscribe to tx updates")
	}
	if _, err = fc.SendAndWaitContext(runCtx, "sub atu all"); err != nil {
		log.Error().Err(err).Msg("Failed to subscribe to atu updates")
	}

	if cfg.Metering {
		resetMetering()
		enableMetering(runCtx, fc)
	}

	setConnState(ConnStateConnected, nil)
	log.Info().Str("ctx", "flexy").Str("proto", "TCP").Msg("Connected to radio")

	<-runCtx.Done()

	setConnState(ConnStateDisconnected, nil)
	fc.Close()
	wg.Wait()
	resetMetering()
	log.Info().Msg("Radio connection closed")

	return runCtx.Err()
}

func main() {
	log.Logger = zerolog.New(
		zerolog.MultiLevelWriter(
			zerolog.ConsoleWriter{Out: os.Stderr},
			logBuf,
		),
	).With().Timestamp().Logger()

	flag.Parse()

	// --setup: run wizard and exit.
	if f := flag.Lookup("setup"); f != nil && f.Value.String() == "true" {
		if runSetup() {
			os.Exit(0)
		}
		os.Exit(1)
	}

	// Load XDG config, then overlay any explicitly-set CLI flags.
	fileCfg, err := config.Load()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load config file; using defaults")
	}
	if fileCfg.IsStale() {
		log.Warn().
			Int("file_version", fileCfg.Version).
			Int("current_version", config.CurrentVersion).
			Str("path", config.Path()).
			Msg("Config file is outdated; run --setup to configure new options")
	}
	applyConfigFile(&fileCfg)

	logLevel, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.Fatal().Str("level", cfg.LogLevel).Msg("Unknown log level")
	}
	zerolog.SetGlobalLevel(logLevel)

	if cfg.Profile != "" && !cfg.Headless {
		log.Fatal().Msg("-profile doesn't make sense without -headless")
	}

	// Interactive TUI: if we're on a TTY, discovering, and not headless.
	interactive := !cfg.Headless && cfg.RadioIP == ":discover:" && isatty.IsTerminal(os.Stdin.Fd())
	if interactive {
		if radio := runTUI(); radio != nil {
			cfg.RadioIP = radio.IP
			log.Info().Str("radio", cfg.RadioIP).Str("nickname", radio.Nickname).Msg("Radio selected")
		} else {
			log.Info().Msg("No radio selected, exiting")
			os.Exit(0)
		}
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigChan:
			log.Info().Str("signal", sig.String()).Msg("Received signal, shutting down")
			rootCancel()
		case <-rootCtx.Done():
		}
	}()

	if err := hamlib.Listen(cfg.Listen); err != nil {
		log.Fatal().Err(err).Msg("Failed to start hamlib server")
	}

	var outerWg sync.WaitGroup
	outerWg.Add(1)
	go func() {
		defer outerWg.Done()
		hamlib.Run(rootCtx)
		log.Info().Msg("Hamlib server exited")
	}()

	if cfg.WebListen != "" {
		outerWg.Add(1)
		go func() {
			defer outerWg.Done()
			runWebServer(rootCtx, cfg.WebListen)
		}()
	}

	if cfg.ProxyListen != "" {
		proxyIP := cfg.ProxyIP
		if proxyIP == "" {
			proxyIP = getLocalIP()
		}
		if proxyIP == "" {
			log.Fatal().Msg("Could not auto-detect proxy IP; specify -proxy-ip explicitly")
		}
		outerWg.Add(1)
		go func() {
			defer outerWg.Done()
			startSmartSDRProxy(rootCtx, cfg.ProxyListen)
		}()
		outerWg.Add(1)
		go func() {
			defer outerWg.Done()
			startDiscoveryRelay(rootCtx, proxyIP)
		}()
	}

	if cfg.ProxyOnly {
		log.Info().Msg("Running in proxy-only mode; flexclient radio connection disabled")
		<-rootCtx.Done()
	} else {
		// Connection loop: reconnects when signaled via web UI or after a failure.
		for {
			connCtx, connCancel := context.WithCancel(rootCtx)

			// Fan reconnectCh into connCancel so a web-triggered reconnect
			// gracefully tears down the current runRadio call.
			go func() {
				select {
				case <-reconnectCh:
					log.Info().Msg("Reconnect requested")
					connCancel()
				case <-connCtx.Done():
				}
			}()

			err := runRadio(connCtx)
			connCancel() // always cancel to release the fan-out goroutine

			if rootCtx.Err() != nil {
				break
			}

			if err != nil && !errors.Is(err, context.Canceled) {
				log.Error().Err(err).Msg("Radio connection failed; retrying in 5s")
				select {
				case <-time.After(reconnectDelay):
				case <-rootCtx.Done():
				}
			}

			if rootCtx.Err() != nil {
				break
			}
		}
	}

	log.Info().Msg("Shutting down...")
	outerWg.Wait()
	log.Info().Msg("Shutdown complete")
}
