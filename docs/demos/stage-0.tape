# Stage 0 demo: empty shell, keyboard navigation, theme picker, help, resize.
# Record with: vhs docs/demos/stage-0.tape  (needs vhs and a built bin/viewdi)

Output docs/demos/stage-0.gif
Set FontSize 14
Set Width 1400
Set Height 820
Set Theme "Everforest Dark Medium"

Hide
Type "make build && clear" Enter
Sleep 2s
Show

Type "./bin/viewdi --mock" Enter
Sleep 1500ms
Type "2" Sleep 700ms
Type "3" Sleep 700ms
Type "4" Sleep 700ms
Tab Sleep 700ms
Tab Sleep 700ms
Type "5" Sleep 700ms
Type "6" Sleep 700ms
Type "1" Sleep 700ms
Type "?" Sleep 2s
Escape Sleep 500ms
Type "T" Sleep 800ms
Down Sleep 600ms
Down Sleep 600ms
Down Sleep 600ms
Enter Sleep 1500ms
Type "3" Sleep 500ms
Type "d" Sleep 1500ms
Type "q"
Sleep 500ms
