package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme uses adaptive colors so the interface remains legible on both light
// and dark terminals. Symbols and text remain present because color is never
// the only status indicator.
type Theme struct {
	Title         lipgloss.Style
	Accent        lipgloss.Style
	Muted         lipgloss.Style
	Selected      lipgloss.Style
	TableHeader   lipgloss.Style
	Success       lipgloss.Style
	Warning       lipgloss.Style
	Error         lipgloss.Style
	Info          lipgloss.Style
	Running       lipgloss.Style
	Pending       lipgloss.Style
	Suspended     lipgloss.Style
	PanelTitle    lipgloss.Style
	ActiveInput   lipgloss.Style
	InactiveInput lipgloss.Style
	Key           lipgloss.Style
	Logo          lipgloss.Style
	Border        lipgloss.Style
	BorderFocused lipgloss.Style
	Tab           lipgloss.Style
	TabActive     lipgloss.Style
}

func DefaultTheme() Theme {
	primary := lipgloss.AdaptiveColor{Light: "#006C8F", Dark: "#67D8EF"}
	secondary := lipgloss.AdaptiveColor{Light: "#5B21B6", Dark: "#C4A7FF"}
	muted := lipgloss.AdaptiveColor{Light: "#5F6B76", Dark: "#7F8D99"}
	selectedBackground := lipgloss.AdaptiveColor{Light: "#DDEEFF", Dark: "#20334A"}
	selectedForeground := lipgloss.AdaptiveColor{Light: "#102A43", Dark: "#F2F8FF"}
	green := lipgloss.AdaptiveColor{Light: "#16794A", Dark: "#69DB9E"}
	yellow := lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#FFD166"}
	red := lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#FF7B72"}
	blue := lipgloss.AdaptiveColor{Light: "#175CD3", Dark: "#79B8FF"}
	border := lipgloss.AdaptiveColor{Light: "#B8C2CC", Dark: "#3E4C59"}
	onAccent := lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0B1220"}

	return Theme{
		Title:         lipgloss.NewStyle().Bold(true).Foreground(primary),
		Accent:        lipgloss.NewStyle().Bold(true).Foreground(secondary),
		Muted:         lipgloss.NewStyle().Foreground(muted),
		Selected:      lipgloss.NewStyle().Bold(true).Foreground(selectedForeground).Background(selectedBackground),
		TableHeader:   lipgloss.NewStyle().Bold(true).Foreground(primary),
		Success:       lipgloss.NewStyle().Foreground(green),
		Warning:       lipgloss.NewStyle().Foreground(yellow),
		Error:         lipgloss.NewStyle().Bold(true).Foreground(red),
		Info:          lipgloss.NewStyle().Foreground(blue),
		Running:       lipgloss.NewStyle().Foreground(blue),
		Pending:       lipgloss.NewStyle().Foreground(yellow),
		Suspended:     lipgloss.NewStyle().Foreground(secondary),
		PanelTitle:    lipgloss.NewStyle().Bold(true).Foreground(primary),
		ActiveInput:   lipgloss.NewStyle().Bold(true).Foreground(primary),
		InactiveInput: lipgloss.NewStyle().Foreground(selectedForeground),
		Key:           lipgloss.NewStyle().Bold(true).Foreground(secondary),
		Logo:          lipgloss.NewStyle().Bold(true).Foreground(onAccent).Background(primary),
		Border:        lipgloss.NewStyle().Foreground(border),
		BorderFocused: lipgloss.NewStyle().Foreground(primary),
		Tab:           lipgloss.NewStyle().Foreground(muted),
		TabActive:     lipgloss.NewStyle().Bold(true).Foreground(onAccent).Background(secondary),
	}
}

func (theme Theme) Status(text, status string) string {
	normalized := strings.ToLower(status)
	switch {
	case strings.Contains(normalized, "failed"), strings.Contains(normalized, "error"), strings.Contains(normalized, "fatal"), strings.Contains(normalized, "unavailable"), normalized == "stale":
		return theme.Error.Render(text)
	case strings.Contains(normalized, "suspended"):
		return theme.Suspended.Render(text)
	case strings.Contains(normalized, "pending"), strings.Contains(normalized, "reconnecting"), strings.Contains(normalized, "partially authorized"):
		return theme.Pending.Render(text)
	case strings.Contains(normalized, "running"), strings.Contains(normalized, "active"):
		return theme.Running.Render(text)
	case strings.Contains(normalized, "fresh"), strings.Contains(normalized, "ready"), strings.Contains(normalized, "succeeded"), strings.Contains(normalized, "deployed"):
		return theme.Success.Render(text)
	default:
		return theme.Muted.Render(text)
	}
}

func (theme Theme) Row(text string, selected bool) string {
	if selected {
		return theme.Selected.Render(text)
	}
	return text
}
