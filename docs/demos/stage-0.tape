# Stage 0 demo: empty shell, keyboard navigation, panes, help, theme picker.
# Record from the repository root with: vhs docs/demos/stage-0.tape

Output docs/demos/stage-0.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

# Build, and isolate config/state so the user's own files never appear.
Hide
Type "make build >/dev/null && export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && clear" Enter
Sleep 8s
Show

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 2s

# Views 1-6, keyboard only.
Type "2" Sleep 900ms
Type "3" Sleep 900ms
Type "4" Sleep 900ms
# Focus moves between panes.
Tab Sleep 900ms
Tab Sleep 900ms
Type "5" Sleep 900ms
Type "6" Sleep 900ms
Type "1" Sleep 900ms

# Help overlay.
Type "?" Sleep 2500ms
Escape Sleep 700ms

# Instance keys are reserved in instance views; no instances yet in Stage 0.
Type "3" Sleep 600ms
Type "d" Sleep 2s

# Theme picker with live preview, then apply.
Type "T" Sleep 1s
Down Sleep 900ms
Down Sleep 900ms
Down Sleep 900ms
Enter Sleep 2s
Type "4" Sleep 1500ms

Type "q"
Sleep 1s
