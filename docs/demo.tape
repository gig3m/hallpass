# Demo GIF for the README. Render with: vhs docs/demo.tape
# It really grants and then revokes a pass, so point it at a connected
# device that has no pass yet (check `hallpass rules` first). On thanos
# that's the fourth row, the Audioengine DAC.

Output docs/demo.gif

Set Shell bash
Set FontFamily "JetBrainsMono Nerd Font"
Set FontSize 15
Set Width 1500
Set Height 860
Set Padding 24
Set Framerate 20
Set TypingSpeed 70ms
Set Theme { "name": "hallpass", "background": "#141317", "foreground": "#ebe6dc", "cursor": "#ff7eb6", "selection": "#25222b", "black": "#141317", "red": "#ff6b6b", "green": "#8be28b", "yellow": "#ffd166", "blue": "#7cc7ff", "magenta": "#ff7eb6", "cyan": "#7cc7ff", "white": "#ebe6dc", "brightBlack": "#7d7787", "brightRed": "#ff6b6b", "brightGreen": "#8be28b", "brightYellow": "#ffd166", "brightBlue": "#7cc7ff", "brightMagenta": "#ff7eb6", "brightCyan": "#7cc7ff", "brightWhite": "#ffffff" }
Env COLORTERM truecolor

Hide
Type "export PS1='\[\e[38;2;139;226;139m\]$ \[\e[0m\]' && clear"
Enter
Sleep 300ms
Show

Type "hallpass"
Sleep 400ms
Enter
Sleep 3s

# select a root-only device
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 2s

# issue a pass
Type "a"
Sleep 3s

# what got written
Tab
Sleep 3s

# revoke it again
Tab
Sleep 800ms
Type "d"
Sleep 1.8s
Type "y"
Sleep 2.5s

Type "q"
Sleep 1.5s
