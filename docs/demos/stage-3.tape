# Stage 3 demo: the Detail view of one instance — Overview with a masked
# secret, live Logs (pause, regex filter, stream), Inspect tree with search,
# Network, Volumes and Resources charts.
# Record from the repository root with: vhs docs/demos/stage-3.tape

Output docs/demos/stage-3.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "make build >/dev/null && export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && clear" Enter
Sleep 15s
Show

Type "./bin/viewdi --mock=logflood --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 3s

# Pick web-api in the Table view and open its detail.
Type "3" Sleep 1s
Type "/web-api" Sleep 500ms Enter Sleep 500ms
Enter Sleep 3s

# Overview: reveal the masked password.
Down 14 Sleep 1s
Type "v" Sleep 2s
Type "v" Sleep 1s

# Logs: 1 000 lines/s; pause, filter errors, follow again.
Tab Sleep 3s
Type "p" Sleep 2s
Type "/ERROR" Sleep 500ms Enter Sleep 3s
Type "S" Sleep 2s
Type "S" Sleep 500ms Type "S" Sleep 500ms Type "S" Sleep 1s
Escape Sleep 500ms
Type "G" Sleep 3s

# Inspect: fold, search, copy a path.
Tab Sleep 2s
Type "/health" Sleep 500ms Enter Sleep 2s
Type "y" Sleep 2s
Escape Sleep 500ms

# Network, Volumes, Resources.
Tab Sleep 3s
Tab Sleep 2s
Tab Sleep 4s
Type "-" Sleep 2s

# Back to the table.
Escape Sleep 2s
Type "q"
Sleep 1s
