package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/radiolabme/flexy/internal/config"
)

// runSetup launches an interactive setup wizard and saves the result to
// the XDG config file. Returns true if the config was saved.
func runSetup() bool {
	c, _ := config.Load()

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
			huh.NewInput().
				Title("Hamlib listen address").
				Description("CAT listen [address]:port").
				Placeholder(":4532").
				Value(&c.Listen),

			huh.NewInput().
				Title("Web UI listen address").
				Description("Leave blank to disable").
				Placeholder(":8080").
				Value(&c.Web),

			huh.NewInput().
				Title("Proxy listen address").
				Description("SmartSDR proxy [address]:port — blank to disable").
				Placeholder("").
				Value(&c.Proxy),
		).Title("Network"),
	).Run()

	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Setup cancelled.")
			return false
		}
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		return false
	}

	if err := config.Save(&c); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
		return false
	}

	fmt.Printf("Config saved to %s\n", config.Path())
	return true
}
