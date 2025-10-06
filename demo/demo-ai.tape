Set PlaybackSpeed 2.0
Set Shell zsh
Set FontSize 15
Set Height 800
Set Width 1200

Sleep 500ms
Type ": 'Run promql-cli with AI support, note $ANTHROPIC_API_KEY is already set ... ->'"
Sleep 2s
Enter
Wait

Type "promql-cli --ai provider=claude,model=claude-opus-4-1-20250805,answers=3 query"
Sleep 2s
Enter
Wait

Type "# Let's scrape local node-exporter (10×2s), grepping for ^node_ metrics ..."
Sleep 2s
Enter
Wait

Type ".scrape http://localhost:9100/metrics 10 2s ^node_"
Sleep 2s
Enter
Sleep 18s
Wait

Type "# Pin eval time to `now`, so that we can keep querying even if time passes ..."
Sleep 2s
Enter
Wait

Type .pinat now
Sleep 2s
Enter
Wait

Type "# Let's see what cpu metrics we got ..."
Sleep 2s
Enter
Wait

Type ".metrics | grep cpu"
Sleep 2s
Enter
Wait

Type "# Let's ask our chosen AI for insights on these cpu metrics ..."
Sleep 2s
Enter
Wait

Type ".ai ask gimme insights for whole cpu usage over the last 20 seconds"
Sleep 2s
Enter
Sleep 8s
Enter
Enter
Wait

Type "# Using 1st suggestion, copy it to the clipboard, then paste it with Ctrl+Y, then (edit+) Enter ..."
Sleep 2s
Enter
Wait

Type ".ai edit 1"
Sleep 2s
Enter
Wait

Ctrl+Y
Enter
Wait

Type "# Let's save collected metrics, for future querying with `.load` ..."
Sleep 2s
Enter
Wait

Type ".save metrics.prom"
Sleep 2s
Enter
Wait

Type "# That's all for this demo, now it's your turn ;)"
Sleep 2s
Enter
Wait

Type ".pinat remove"
Sleep 2s
Enter
Wait

Sleep 15s
Ctrl+D
