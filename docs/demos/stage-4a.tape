# Stage 4a demo: lifecycle actions on the mock engines — stop and start with
# the pending state, a bulk restart with confirmation, the delete dialog,
# and the Jobs view.
# Record from the repository root with: vhs docs/demos/stage-4a.tape

Output docs/demos/stage-4a.gif
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

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 3s

# Stop and start redis-cache: the row shows the operation in flight.
Type "3" Sleep 1s
Type "/redis" Sleep 300ms Enter Sleep 1s
Type "x" Sleep 400ms
Sleep 3s
Type "s" Sleep 3s

# Clear the filter, mark two containers and restart them together.
Escape Sleep 1s
Down 4 Sleep 300ms
Space Sleep 300ms
Down 3 Sleep 300ms
Space Sleep 1s
Type "R" Sleep 2s
Type "y" Sleep 4s

# Delete a stopped container: the dialog starts on Cancel.
Type "/old" Sleep 300ms Enter Sleep 1s
Type "d" Sleep 2s
Tab Sleep 1s
Enter Sleep 3s
Escape Sleep 500ms

# Delete a running one: its name must be typed.
Type "/web-frontend" Sleep 300ms Enter Sleep 1s
Type "d" Sleep 1s
Type "web-front" Sleep 1500ms
Escape Sleep 1s
Escape Sleep 500ms

# The Jobs view lists every operation.
Type "5" Sleep 4s
Type "q"
Sleep 1s
