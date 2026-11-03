# Stage 9b demo: the command palette, the action menu on enter, a shell and
# raising a limit, on the mock engines.
# Record from the repository root with: vhs docs/demos/stage-9b.tape

Output docs/demos/stage-9b.gif
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
Sleep 4s

# The palette: a machine and a verb, with the key it teaches.
Ctrl+K Sleep 1s
Type "restart web" Sleep 1500ms
Escape Sleep 500ms

# enter on a machine opens the action menu.
Type "2" Sleep 1s
Type "v" Sleep 500ms
Down Down Down Down Sleep 500ms
Enter Sleep 2s
Down Down Sleep 1s
Escape Sleep 500ms

# Raise a limit.
Type "m" Sleep 2s
Escape Sleep 500ms

# Go anywhere from the palette.
Ctrl+K Sleep 500ms
Type "activity" Sleep 1s
Enter Sleep 2s
Type "q"
