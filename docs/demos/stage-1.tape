# Stage 1 demo: both engines in the Table view, filter, sort, project picker,
# and an Incus outage with automatic recovery (--mock=chaos).
# Record from the repository root with: vhs docs/demos/stage-1.tape

Output docs/demos/stage-1.gif
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

Type "./bin/viewdi --mock=chaos --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 3s
Type "3" Sleep 1s

# Cursor, sort, engine filter.
Down 3 Sleep 600ms
Type "o" Sleep 800ms
Type "o" Sleep 800ms
Type "O" Sleep 800ms
Type "e" Sleep 800ms
Type "e" Sleep 800ms
Type "e" Sleep 800ms

# Fuzzy filter: the prompt captures every key, esc clears.
Type "/" Sleep 400ms
Type "wpi" Sleep 1s
Enter Sleep 800ms
Escape Sleep 800ms

# Incus project picker.
Type "P" Sleep 800ms
Down Sleep 500ms
Escape Sleep 500ms

# The chaos scenario stops Incus at 5 s: banner, countdown, stale rows,
# then automatic recovery.
Sleep 20s
Type "q"
Sleep 1s
