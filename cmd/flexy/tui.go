package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/radiolabme/flexy/internal/discovery"
)

// TUI states
type tuiState int

const (
	tuiDiscovering tuiState = iota
	tuiSelectRadio
)

// Messages
type radiosMsg []discovery.Radio

// tuiResult holds the outcome of the TUI startup flow.
type tuiResult struct {
	Radio *discovery.Radio
	Err   error
}

type tuiModel struct {
	state   tuiState
	radios  []discovery.Radio
	cursor  int
	spinner spinner.Model
	scanCh  <-chan []discovery.Radio
	cancel  context.CancelFunc
	result  tuiResult
	width   int
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).MarginTop(1)
)

func newTUIModel() tuiModel {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))

	ctx, cancel := context.WithCancel(context.Background())
	ch := discovery.Scan(ctx, 5*time.Second)

	return tuiModel{
		state:   tuiDiscovering,
		spinner: s,
		scanCh:  ch,
		cancel:  cancel,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForRadios(m.scanCh))
}

func waitForRadios(ch <-chan []discovery.Radio) tea.Cmd {
	return func() tea.Msg {
		radios, ok := <-ch
		if !ok {
			return radiosMsg(nil)
		}
		return radiosMsg(radios)
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancel()
			return m, tea.Quit

		case "up", "k":
			if m.state == tuiSelectRadio && m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.state == tuiSelectRadio && m.cursor < len(m.radios)-1 {
				m.cursor++
			}
		case "enter":
			if m.state == tuiSelectRadio && len(m.radios) > 0 {
				m.cancel()
				r := m.radios[m.cursor]
				m.result.Radio = &r
				return m, tea.Quit
			}
		}

	case radiosMsg:
		if msg == nil {
			return m, nil
		}
		m.radios = msg
		if len(m.radios) > 0 {
			m.state = tuiSelectRadio
			if m.cursor >= len(m.radios) {
				m.cursor = 0
			}
		}
		return m, waitForRadios(m.scanCh)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m tuiModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("flexy") + "\n\n")

	switch m.state {
	case tuiDiscovering:
		b.WriteString(m.spinner.View() + " Scanning for radios...\n")
		b.WriteString(helpStyle.Render("q: quit"))

	case tuiSelectRadio:
		b.WriteString("Select a radio:\n\n")
		for i := range m.radios {
			r := &m.radios[i]
			label := formatRadioLabel(r)
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("▸ " + label))
			} else {
				b.WriteString("  " + label)
			}
			b.WriteString("\n")
		}
		b.WriteString(helpStyle.Render("↑/↓: navigate • enter: select • q: quit"))
	}

	return b.String()
}

func formatRadioLabel(r *discovery.Radio) string {
	nick := r.Nickname
	if nick == "" {
		nick = r.Serial
	}
	status := dimStyle.Render(fmt.Sprintf("(%s %s %s)", r.Model, r.Version, r.Status))
	inuse := ""
	if r.Inuse != "" {
		inuse = dimStyle.Render(" [in use: " + r.Inuse + "]")
	}
	return fmt.Sprintf("%s @ %s %s%s", nick, r.IP, status, inuse)
}

// runTUI launches the interactive startup TUI. It blocks until the user
// selects a radio or quits. Returns the selected radio or nil.
func runTUI() *discovery.Radio {
	m := newTUIModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return nil
	}
	fm := final.(tuiModel) //nolint:errcheck // bubbletea returns our model type
	return fm.result.Radio
}
