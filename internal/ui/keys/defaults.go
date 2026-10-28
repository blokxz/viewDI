package keys

// Global actions.
const (
	ActViewAdmin     = "view_admin"
	ActViewResources = "view_resources"
	ActViewTable     = "view_table"
	ActViewTree      = "view_tree"
	ActViewJobs      = "view_jobs"
	ActViewUpdates   = "view_updates"
	ActNextPane      = "next_pane"
	ActPrevPane      = "prev_pane"
	ActOpen          = "open"
	ActBack          = "back"
	ActFilter        = "filter"
	ActEngine        = "engine_filter"
	ActNew           = "new"
	ActImport        = "import"
	ActCheckUpdates  = "check_updates"
	ActProject       = "project"
	ActTheme         = "theme"
	ActHelp          = "help"
	ActReconnect     = "reconnect"
	ActDebug         = "debug"
	ActSuspend       = "suspend"
	ActQuit          = "quit"
)

// Instance actions.
const (
	ActStart   = "start"
	ActStop    = "stop"
	ActRestart = "restart"
	ActPause   = "pause"
	ActDelete  = "delete"
)

// Modal actions shared by modal scopes.
const (
	ActUp       = "up"
	ActDown     = "down"
	ActConfirm  = "confirm"
	ActCancel   = "cancel"
	ActClose    = "close"
	ActActivate = "activate"
	ActFocus    = "focus"
)

// ViewActions maps the global view-switch actions to view numbers 1–6.
var ViewActions = [6]string{
	ActViewAdmin, ActViewResources, ActViewTable, ActViewTree, ActViewJobs, ActViewUpdates,
}

// Defaults returns the built-in key map, matching docs/03-mockups.md.
// Local scopes that later stages wire up are declared now so the conflict
// detector guards the whole documented map from day one.
func Defaults() []Def {
	return []Def{
		{Global, ActViewAdmin, []string{"1"}, "admin dashboard"},
		{Global, ActViewResources, []string{"2"}, "resources"},
		{Global, ActViewTable, []string{"3"}, "table"},
		{Global, ActViewTree, []string{"4"}, "tree"},
		{Global, ActViewJobs, []string{"5"}, "jobs"},
		{Global, ActViewUpdates, []string{"6"}, "updates"},
		{Global, ActNextPane, []string{"tab"}, "next pane"},
		{Global, ActPrevPane, []string{"shift+tab"}, "previous pane"},
		{Global, ActOpen, []string{"enter"}, "open / confirm"},
		{Global, ActBack, []string{"esc"}, "back / close"},
		{Global, ActFilter, []string{"/"}, "filter"},
		{Global, ActEngine, []string{"e"}, "engine filter"},
		{Global, ActNew, []string{"n"}, "new container"},
		{Global, ActImport, []string{"i"}, "import"},
		{Global, ActCheckUpdates, []string{"U"}, "check for updates"},
		{Global, ActProject, []string{"P"}, "incus project"},
		{Global, ActTheme, []string{"T"}, "theme picker"},
		{Global, ActHelp, []string{"?"}, "help"},
		{Global, ActReconnect, []string{"ctrl+r"}, "reconnect / resync"},
		{Global, ActDebug, []string{"ctrl+d"}, "debug overlay (--debug)"},
		{Global, ActSuspend, []string{"ctrl+z"}, "suspend"},
		{Global, ActQuit, []string{"q", "ctrl+c"}, "quit"},

		{Instance, ActStart, []string{"s"}, "start"},
		{Instance, ActStop, []string{"x"}, "stop"},
		{Instance, ActRestart, []string{"R"}, "restart"},
		{Instance, ActPause, []string{"F"}, "pause / unpause"},
		{Instance, ActDelete, []string{"d"}, "delete"},

		{Table, "sort", []string{"o"}, "cycle sort column"},
		{Table, "sort_reverse", []string{"O"}, "reverse sort"},
		{Table, "mark", []string{"space"}, "mark row"},

		{Resources, "sort", []string{"o"}, "cycle sort column"},
		{Resources, "sort_reverse", []string{"O"}, "reverse sort"},
		{Resources, "rate_up", []string{"+"}, "faster refresh"},
		{Resources, "rate_down", []string{"-"}, "slower refresh"},

		{Tree, "up", []string{"up", "k"}, "previous node"},
		{Tree, "down", []string{"down", "j"}, "next node"},
		{Tree, "expand", []string{"right", "l"}, "expand / first child"},
		{Tree, "collapse", []string{"left", "h"}, "collapse / parent"},
		{Tree, "prev_sibling", []string{"["}, "previous sibling"},
		{Tree, "next_sibling", []string{"]"}, "next sibling"},
		{Tree, "toggle", []string{"space"}, "toggle node"},
		{Tree, "expand_all", []string{"*"}, "expand all below"},
		{Tree, "collapse_all", []string{"-"}, "collapse all"},
		{Tree, "home", []string{"home"}, "first node"},
		{Tree, "end", []string{"end"}, "last node"},
		{Tree, "page_up", []string{"pgup"}, "page up"},
		{Tree, "page_down", []string{"pgdown"}, "page down"},
		{Tree, "next_occurrence", []string{"J"}, "next occurrence"},
		{Tree, "grouping", []string{"g"}, "cycle grouping"},

		{Updates, "pull", []string{"p"}, "pull image"},
		{Updates, "recreate", []string{"c"}, "recreate"},
		{Updates, "refresh", []string{"f"}, "refresh image"},
		{Updates, "snooze", []string{"z"}, "snooze"},
		{Updates, "ignore", []string{"I"}, "ignore version"},
		{Updates, "sort", []string{"o"}, "cycle sort column"},
		{Updates, "sort_reverse", []string{"O"}, "reverse sort"},

		{Filter, "next_match", []string{"ctrl+n"}, "next match"},
		{Filter, "prev_match", []string{"ctrl+p"}, "previous match"},

		{Logs, "pause", []string{"p"}, "pause / resume"},
		{Logs, "wrap", []string{"w"}, "wrap"},
		{Logs, "timestamps", []string{"t"}, "timestamps"},
		{Logs, "stream", []string{"S"}, "stream all/out/err"},
		{Logs, "end", []string{"G"}, "jump to end"},
		{Logs, "top", []string{"gg"}, "jump to top"},

		{Overview, "reveal", []string{"v"}, "reveal masked value"},

		{Confirm, ActConfirm, []string{"y"}, "confirm"},
		{Confirm, ActCancel, []string{"n", "esc"}, "cancel"},
		{Confirm, ActActivate, []string{"enter"}, "press focused button"},
		{Confirm, ActFocus, []string{"tab", "shift+tab", "left", "right", "h", "l"}, "switch button"},

		{Picker, ActUp, []string{"up", "k"}, "previous"},
		{Picker, ActDown, []string{"down", "j"}, "next"},
		{Picker, ActConfirm, []string{"enter"}, "apply"},
		{Picker, ActCancel, []string{"esc"}, "cancel"},

		{Help, ActUp, []string{"up", "k"}, "scroll up"},
		{Help, ActDown, []string{"down", "j"}, "scroll down"},
		{Help, ActClose, []string{"esc", "?", "q"}, "close"},
	}
}
