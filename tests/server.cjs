const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');

http.createServer((req, res) => {
  const name = { '/': 'index.html', '/app.js': 'app.js', '/styles.css': 'styles.css', '/favicon.svg': 'favicon.svg' }[req.url];
  if (!name) { res.writeHead(404).end(); return; }
  res.setHeader('Content-Type', name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : name.endsWith('.svg') ? 'image/svg+xml' : 'text/html');
  fs.createReadStream(path.join(__dirname, '../web', name)).pipe(res);
}).listen(4173, '127.0.0.1');
