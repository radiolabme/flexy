package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/radiolabme/flexy/internal/config"
)

const proxyIPOther = "__other__"

type localAddr struct {
	IP    string
	Label string
}

func localUnicastAddrs() []localAddr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []localAddr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifAddrs, _ := iface.Addrs()
		for _, addr := range ifAddrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			ipStr := ip4.String()
			label := ipStr + " (" + iface.Name + ")"
			if isTailscaleIP(ip4) {
				label = ipStr + " (tailscale)"
			}
			addrs = append(addrs, localAddr{IP: ipStr, Label: label})
		}
	}
	return addrs
}

func listenIPOptions(current string) ([]huh.Option[string], string) {
	addrs := localUnicastAddrs()
	opts := make([]huh.Option[string], 0, len(addrs)+3)
	opts = append(opts, huh.NewOption("All interfaces", ""))
	for _, a := range addrs {
		opts = append(opts, huh.NewOption(a.Label, a.IP))
	}
	opts = append(opts, huh.NewOption("Other (enter manually)", proxyIPOther))

	selected := ""
	if current != "" {
		found := false
		for _, a := range addrs {
			if a.IP == current {
				found = true
				break
			}
		}
		if found {
			selected = current
		} else {
			selected = proxyIPOther
		}
	}
	return opts, selected
}

func advertiseIPOptions(current string) ([]huh.Option[string], string) {
	addrs := localUnicastAddrs()
	opts := make([]huh.Option[string], 0, len(addrs)+2)
	opts = append(opts, huh.NewOption("Auto-detect", ""))
	for _, a := range addrs {
		opts = append(opts, huh.NewOption(a.Label, a.IP))
	}
	opts = append(opts, huh.NewOption("Other (enter manually)", proxyIPOther))

	selected := ""
	if current != "" {
		found := false
		for _, a := range addrs {
			if a.IP == current {
				found = true
				break
			}
		}
		if found {
			selected = current
		} else {
			selected = proxyIPOther
		}
	}
	return opts, selected
}

// splitHostPort splits an address like "192.168.1.5:4992" into host and port.
// Handles bare ":port" (empty host) and empty string (both empty).
func splitHostPort(addr string) (string, string) {
	if addr == "" {
		return "", ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, ""
	}
	return host, port
}

func runSetup() bool {
	c, _ := config.Load()

	// Hamlib listen
	catHost, catPort := splitHostPort(c.Listen)
	catOpts, catIPSel := listenIPOptions(catHost)
	customCatIP := ""
	if catIPSel == proxyIPOther {
		customCatIP = catHost
	}

	// Web UI listen
	webHost, webPort := splitHostPort(c.Web)
	webOpts, webIPSel := listenIPOptions(webHost)
	customWebIP := ""
	if webIPSel == proxyIPOther {
		customWebIP = webHost
	}

	// Proxy listen
	proxyHost, proxyPort := splitHostPort(c.Proxy)
	proxyOpts, proxyIPSel := listenIPOptions(proxyHost)
	customProxyIP := ""
	if proxyIPSel == proxyIPOther {
		customProxyIP = proxyHost
	}

	// Proxy advertised IP
	advIPOpts, advIPSel := advertiseIPOptions(c.ProxyIP)
	customAdvIP := ""
	if advIPSel == proxyIPOther {
		customAdvIP = c.ProxyIP
	}

	// UDP port for VITA packets
	udpPortStr := ""
	if c.UDPPort != 0 {
		udpPortStr = strconv.Itoa(c.UDPPort)
	}

	// Log level
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}

	headlessStr := "interactive"
	if c.Headless {
		headlessStr = "headless"
	}

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Radio").
				Description("IP address, :discover:, or serial number").
				Placeholder(":discover:").
				Value(&c.Radio),

			huh.NewInput().
				Title("Station name").
				Description("SmartSDR station to bind to or create").
				Placeholder("Flex").
				Value(&c.Station),

			huh.NewInput().
				Title("Slice").
				Description("Slice letter to control (A, B, C, ...)").
				Placeholder("A").
				Value(&c.Slice),
		).Title("Radio Connection"),

		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Hamlib listen IP").
				Description("CAT server bind address").
				Options(catOpts...).
				Value(&catIPSel),
		).Title("Hamlib (CAT)"),

		huh.NewGroup(
			huh.NewInput().
				Title("Hamlib listen IP").
				Description("Enter the IP address to bind").
				Value(&customCatIP),
		).WithHideFunc(func() bool { return catIPSel != proxyIPOther }),

		huh.NewGroup(
			huh.NewInput().
				Title("Hamlib listen port").
				Placeholder("4532").
				Value(&catPort),
		),

		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Web UI listen IP").
				Description("Web server bind address (leave port blank to disable)").
				Options(webOpts...).
				Value(&webIPSel),
		).Title("Web UI"),

		huh.NewGroup(
			huh.NewInput().
				Title("Web UI listen IP").
				Description("Enter the IP address to bind").
				Value(&customWebIP),
		).WithHideFunc(func() bool { return webIPSel != proxyIPOther }),

		huh.NewGroup(
			huh.NewInput().
				Title("Web UI listen port").
				Description("Blank to disable").
				Placeholder("8080").
				Value(&webPort),
		),

		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Proxy listen IP").
				Description("SmartSDR proxy bind address").
				Options(proxyOpts...).
				Value(&proxyIPSel),
		).Title("SmartSDR Proxy"),

		huh.NewGroup(
			huh.NewInput().
				Title("Proxy listen IP").
				Description("Enter the IP address to bind").
				Value(&customProxyIP),
		).WithHideFunc(func() bool { return proxyIPSel != proxyIPOther }),

		huh.NewGroup(
			huh.NewInput().
				Title("Proxy listen port").
				Description("Blank to disable proxy").
				Placeholder("4992").
				Value(&proxyPort),

			huh.NewSelect[string]().
				Title("Proxy advertised IP").
				Description("IP to advertise in discovery broadcasts").
				Options(advIPOpts...).
				Value(&advIPSel),
		),

		huh.NewGroup(
			huh.NewInput().
				Title("Proxy advertised IP").
				Description("Enter the IP address to advertise").
				Value(&customAdvIP),
		).WithHideFunc(func() bool { return advIPSel != proxyIPOther }),

		huh.NewGroup(
			huh.NewInput().
				Title("UDP port").
				Description("VITA-49 listen port (blank for random free port)").
				Placeholder("0").
				Value(&udpPortStr),

			huh.NewSelect[string]().
				Title("Log level").
				Options(
					huh.NewOption("debug", "debug"),
					huh.NewOption("info", "info"),
					huh.NewOption("warn", "warn"),
					huh.NewOption("error", "error"),
				).
				Value(&c.LogLevel),

			huh.NewSelect[string]().
				Title("Startup mode").
				Description("Interactive shows the TUI; headless runs as a background service").
				Options(
					huh.NewOption("Interactive (TUI)", "interactive"),
					huh.NewOption("Headless (background/service)", "headless"),
				).
				Value(&headlessStr),
		).Title("Advanced"),
	).Run()

	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Setup cancelled.")
			return false
		}
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		return false
	}

	// Reassemble listen addresses.
	catIP := catIPSel
	if catIPSel == proxyIPOther {
		catIP = customCatIP
	}
	c.Listen = net.JoinHostPort(catIP, catPort)

	webIP := webIPSel
	if webIPSel == proxyIPOther {
		webIP = customWebIP
	}
	if webPort != "" {
		c.Web = net.JoinHostPort(webIP, webPort)
	} else {
		c.Web = ""
	}

	listenIP := proxyIPSel
	if proxyIPSel == proxyIPOther {
		listenIP = customProxyIP
	}
	if proxyPort != "" {
		c.Proxy = net.JoinHostPort(listenIP, proxyPort)
	} else {
		c.Proxy = ""
	}

	if advIPSel == proxyIPOther {
		c.ProxyIP = customAdvIP
	} else {
		c.ProxyIP = advIPSel
	}

	// UDP port
	if udpPortStr != "" {
		c.UDPPort, _ = strconv.Atoi(udpPortStr)
	} else {
		c.UDPPort = 0
	}

	c.Headless = headlessStr == "headless"

	c.Version = config.CurrentVersion

	if err := config.Save(&c); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
		return false
	}

	fmt.Printf("Config saved to %s\n", config.Path())
	return true
}
