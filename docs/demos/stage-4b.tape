# Stage 4b demo: create and import on the mock engines — a Docker container
# with local image suggestions and pull progress, an Incus VM from an
# images: search, and an image import through the file picker.
# Record from the repository root with: vhs docs/demos/stage-4b.tape

Output docs/demos/stage-4b.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "make build >/dev/null && export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && mkdir -p /tmp/viewdi-demo && head -c 52428800 /dev/zero > /tmp/viewdi-demo/custom-app.tar.gz && cd /tmp/viewdi-demo && clear" Enter
Sleep 15s
Show

Type "$OLDPWD/bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 3s

# Create a Docker container: suggestions from local images, ports, a secret.
Type "n" Sleep 1500ms
Enter Sleep 1500ms
Type "ngi" Sleep 800ms
Ctrl+N Sleep 1500ms
Ctrl+U Sleep 300ms
Type "caddy:2" Sleep 500ms
Tab Sleep 300ms
Space Sleep 800ms
Tab Sleep 300ms
Type "new-web" Sleep 300ms
Tab Sleep 300ms
Type "8081:80" Sleep 300ms
Tab Sleep 300ms
Type "API_TOKEN=s3cret" Sleep 300ms
Tab Sleep 1500ms
Enter Sleep 1s
Type "5" Sleep 4s

# Create an Incus VM from a remote image found with ctrl+f.
Type "n" Sleep 1s
Down Sleep 300ms
Enter Sleep 1500ms
Type "ubuntu" Sleep 300ms
Ctrl+F Sleep 1500ms
Ctrl+N Sleep 1s
Tab Sleep 300ms
Type "lab-vm" Sleep 300ms
Tab Sleep 300ms
Right Sleep 800ms
Tab Tab Sleep 300ms
Type "2" Sleep 300ms
Tab Sleep 300ms
Type "2GiB" Sleep 1500ms
Enter Sleep 4s

# Import an Incus image through the file picker.
Type "i" Sleep 1500ms
Down 3 Sleep 300ms
Enter Sleep 1500ms
Down Sleep 500ms
Enter Sleep 1500ms
Type "custom/app" Sleep 1s
Enter Sleep 4s

# The Table lists the new instances.
Type "3" Sleep 3s
Type "q"
Sleep 1s
