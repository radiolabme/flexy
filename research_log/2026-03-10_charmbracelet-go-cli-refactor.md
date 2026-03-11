# Research: Charmbracelet Ecosystem & Go CLI Refactor for FlexRadio Proxy
Started: 2026-03-10T23:54:59-07:00 | Status: in_progress

## Problem
Refactor "flexy" (FlexRadio proxy, Go 1.21) from bare flag package to modern TUI/CLI architecture. Need to determine:
1. Charmbracelet ecosystem libraries (bubbletea, lipgloss, bubbles, huh, log) - versions & Go requirements
2. XDG config library for cross-platform config management
3. CLI framework choice (cobra vs lighter alternatives)
4. Design patterns for multi-mode operation (TUI/web/non-interactive)
5. UX pattern for async device discovery with spinner → selectable list

## Awesome Lists Checked

Search 1: 'awesome go' | fresh: none | 2026-03-10T23:55 | findings: Found github.com/avelino/awesome-go - comprehensive curated list
- Confirmed existence of charmbracelet/bubbletea, lipgloss, bubbles under "Advanced Console UIs"
- Found cobra under "Standard CLI" 
- Found adrg/xdg under "Configuration"

## Searches

Search 1: 'charmbracelet bubbletea lipgloss bubbles huh log versions' | fresh: none | 2026-03-10T23:55:35-07:00 | findings: fetched GitHub pages for all core Charmbracelet libraries

[1] Charmbracelet Bubbletea v2.0.2
https://github.com/charmbracelet/bubbletea | 2026-03-10 | GitHub | quality: high
Key insights:
- Latest version: v2.0.2 (released 2 days ago)
- Import path: charm.land/bubbletea/v2
- Go 1.21+ required (inferred from examples using 1.21+ features)
- TUI framework based on The Elm Architecture
- Features: cell-based renderer, keyboard/mouse handling, native clipboard support
- 40.5k stars, actively maintained, production-ready
Summary: Bubbletea is the core TUI framework from Charmbracelet. Version 2 was just released with breaking changes (upgrade guide available). Uses declarative view model similar to React/Elm. Well-suited for both simple and complex terminal apps, inline or full-window. High community adoption with 18.6k dependent projects.

[2] Charmbracelet Lipgloss v2.0.1
https://github.com/charmbracelet/lipgloss | 2026-03-10 | GitHub | quality: high
Key insights:
- Latest version: v2.0.1 (released 2 days ago)
- Import path: charm.land/lipgloss/v2
- Styling library for terminal layouts
- Features: colors with auto-downsampling, borders, padding/margins, alignment, tables, lists, trees
- Supports TrueColor (24-bit), ANSI 256 (8-bit), ANSI 16 (4-bit), and 1-bit ASCII
- Hyperlink support, underline styles (curly, dotted, dashed)
- 10.8k stars
Summary: CSS-like declarative styling for terminal UIs. Handles color profile detection and downsampling automatically. Includes sub-packages for rendering tables, lists, and trees. Works standalone or with Bubbletea.

[3] Charmbracelet Bubbles v2.0.0  
https://github.com/charmbracelet/bubbles | 2026-03-10 | GitHub | quality: high
Key insights:
- Latest version: v2.0.0 (released 2 weeks ago)
- Import path: charm.land/bubbles/v2
- TUI component library (primitives for Bubbletea)
- Components: textinput, textarea, spinner, table, progress, paginator, viewport, list, filepicker, timer, stopwatch, help, key
- Spinner: 11 built-in styles, customizable frames
- List: pagination, fuzzy filtering, auto-generated help
- Viewport: high-performance mode for alt screen buffer
- 8k stars, used in production (Glow, Soft Serve, many others)
Summary: Batteries-included components for Bubbletea apps. Provides common UI patterns (inputs, selection lists, spinners) so you don't have to rebuild them. All components are tea.Model implementations.

[4] Charmbracelet Huh v2.0.3
https://github.com/charmbracelet/huh | 2026-03-10 | GitHub | quality: high
Key insights:
- Latest version: v2.0.3 (released 12 hours ago)
- Import path: charm.land/huh/v2
- Form and prompt library, standalone or Bubbletea-embedded
- Field types: Input, Text (multiline), Select, MultiSelect, Confirm, FilePicker
- Features: validation, accessible mode for screen readers, theming (Charm, Dracula, Catppuccin, Base16)
- Dynamic forms: TitleFunc, OptionsFunc to recompute based on previous fields
- Includes standalone spinner package for post-submission feedback
- 6.7k stars
Summary: High-level form abstraction over Bubbletea. Use standalone for quick prompts or embed in larger Bubbletea apps. Handles validation, accessibility, and theming. Good for onboarding flows, config wizards, user input collection.

[5] Charmbracelet Log v2.0.0
https://github.com/charmbracelet/log | 2026-03-10 | GitHub | quality: high
Key insights:
- Latest version: v2.0.0 (released 2 days ago)  
- Import path: github.com/charmbracelet/log (also charm.land/log/v2?)
- Structured logger with leveled logging (Debug, Info, Warn, Error, Fatal)
- Formatters: Text (styled), JSON, Logfmt
- Uses Lipgloss for styling, auto-disables on non-TTY
- slog.Handler implementation for Go 1.21+ slog integration
- Standard log adapter available
- Global logger + custom logger instances (log.New())
- 3.2k stars
Summary: Colorful, human-readable leveled logger. Alternative to zerolog with better terminal styling. Supports standard library slog interface. Good for CLI apps where log readability matters more than raw performance.

[6] adrg/xdg v0.5.3
https://github.com/adrg/xdg | 2026-03-10 | GitHub | quality: high
Key insights:
- Latest version: v0.5.3 (Oct 31, 2024)
- Import path: github.com/adrg/xdg
- XDG Base Directory Specification implementation
- Cross-platform: Unix, macOS, Windows, Plan 9
- Windows: uses Known Folders API (~LOCALAPPDATA, ~APPDATA)
- macOS: ~/Library/Application Support (config/data), ~/Library/Caches
- Helper methods: ConfigFile(), SearchConfigFile(), DataFile(), etc.
- Includes user directories (Desktop, Documents, Downloads, etc.)
- Supports non-standard XDG_BIN_HOME
- 964 stars, 5.6k dependents, actively maintained (last commit 3 weeks ago)
Summary: Most popular Go XDG library. Well-maintained with comprehensive platform support. Automatically handles OS-specific paths (Windows Known Folders, macOS Library dirs). Clean API for creating and finding config files.

[7] OpenPeeDeeP/xdg v2.0.0
https://github.com/OpenPeeDeeP/xdg | 2026-03-10 | GitHub | quality: medium
Key insights:
- Last update: 6 years ago (2018)
- Import path: github.com/OpenPeeDeeP/xdg
- Requires vendor/application name on initialization
- BSD-3-Clause license (vs MIT for adrg/xdg)
- 80 stars, 3 contributors
- No go.mod until module support added 6 years ago
- Appends Vendor/Application to paths automatically
Summary: Older, less maintained XDG library. Hasn't been updated in 6 years. Different API design (requires vendor/app name upfront). Given lack of maintenance and smaller community, adrg/xdg is the better choice.

Search 2: 'adrg xdg OpenPeeDeeBase XDG comparison' | fresh: none | 2026-03-10T23:55:40-07:00 | findings: fetched both XDG library homepages for detailed comparison

## Sources

## Approaches

## Recommendation

## Implementation

## Risks

METRICS: searches=0 fetches=0 high_quality=0 ratio=0.0
CHECKS: [ ] freshness [ ] went_deep [ ] found_outlier [ ] checked_awesome

## Feedback
usefulness: | implemented: | result: | notes:
