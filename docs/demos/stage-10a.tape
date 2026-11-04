# Stage 10a demo: the Docker Hub browser (S in the Library) on the mock engines — a
# search, a favourite, the most downloaded official images and the favourites tab.
# Record from the repository root, with the binary already built (make build):
#   vhs docs/demos/stage-10a.tape

Output docs/demos/stage-10a.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && clear" Enter
Sleep 1s
Show

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 4s

# The Library, and S opens the Docker Hub browser.
Type "3" Sleep 1500ms
Type "S" Sleep 1500ms

# Search: type, enter, mark one as a favourite.
Type "grafana" Sleep 800ms Enter Sleep 2s
Ctrl+F Sleep 1500ms
Type "x" Sleep 300ms Backspace Sleep 300ms
Ctrl+U Sleep 500ms
Type "nginx" Sleep 800ms Enter Sleep 2s
Down Sleep 600ms Ctrl+F Sleep 1500ms

# The most downloaded official images, most pulled first.
Tab Sleep 3s
Down Sleep 600ms Down Sleep 600ms Ctrl+F Sleep 1500ms

# The favourites: no network, kept between sessions.
Tab Sleep 3s

# enter runs the highlighted image through the create form.
Enter Sleep 3s
Escape Sleep 500ms
Type "q"
