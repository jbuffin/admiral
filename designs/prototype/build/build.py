import re, sys, pathlib

src = pathlib.Path(sys.argv[1]).read_text()
runtime = pathlib.Path(sys.argv[2]).read_text()
out = pathlib.Path(sys.argv[3])

xdc = re.search(r'<x-dc>(.*)</x-dc>', src, re.S).group(1)
helmet = re.search(r'<helmet>(.*?)</helmet>', xdc, re.S).group(1)
template = re.sub(r'<helmet>.*?</helmet>', '', xdc, flags=re.S).strip()
script = re.search(r'<script type="text/x-dc"[^>]*>(.*?)</script>', src, re.S).group(1)

page = f'''<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>Admiral prototype</title>
<!--
  Admiral clickable prototype. Open this file in a browser.
  Scenes: ?scene=harbor (default), ?scene=launch, ?scene=bridge, ?scene=tablet,
  ?scene=inbox, ?scene=inbox-bridge, ?scene=inbox-empty, ?scene=crew,
  ?scene=crew-bridge, ?scene=crew-attn, ?scene=crew-dismiss, ?scene=ships, ?scene=orders,
  ?scene=phone-launch (ships, orders and phone-launch are phone scenes; elsewhere they match harbor),
  ?scene=chat, ?scene=chat-terminal, ?scene=chat-new, ?scene=settings
  Generated from designs/canvas/Main.dc.html by the design handoff; edit the
  .dc.html source, not this file.
-->
{helmet.strip()}
<script src="vendor/react.production.min.js"></script>
<script src="vendor/react-dom.production.min.js"></script>
</head>
<body>
<div id="root"></div>
<template id="dc-template">
{template}
</template>
<script>
{runtime}
</script>
<script>
{script.strip()}
mountDC(Component);
</script>
</body>
</html>
'''
out.write_text(page)
print('wrote', out, len(page))
