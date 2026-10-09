// Fails when the client calls an API route the demo does not answer.
//
// The demo at /demo is the real client with Core played in the browser
// (src/demo/server.ts). A feature whose route the demo does not know shows an
// error there instead of itself, and nobody notices until someone opens the
// demo. This reads every call the client makes and every route the demo
// answers, from the sources, and says which calls are left without an answer.
//
// Run with `npm run check:demo`, or `make web-demo-check`.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../src', import.meta.url));

function sources(directory) {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) return name === 'demo' ? [] : sources(path);
    return /\.(ts|tsx)$/.test(name) && !name.endsWith('.d.ts') ? [path] : [];
  });
}

// api.get<T>(`/api/v1/projects/${id}/tasks?x=${y}`) and api.post('/api/v1/me/presence', …),
// with the generic and the line break that may sit between the call and its path.
const CALL = /\bapi\.(get|post|patch|put|delete|upload)\s*(?:<(?:[^<>]|<[^<>]*>)*>)?\s*\(\s*(['`])(\/api\/[^'`]*)\2/g;
const METHOD = { get: 'GET', post: 'POST', patch: 'PATCH', put: 'PUT', delete: 'DELETE', upload: 'POST' };

const calls = [];
for (const file of sources(root)) {
  const text = readFileSync(file, 'utf8');
  for (const match of text.matchAll(CALL)) {
    // A template part becomes a sample identifier; a ternary inside one, such
    // as `${archived ? 'archive' : 'restore'}`, becomes each of its branches.
    const pattern = /\$\{([^}]*)\}/g;
    const path = match[3].split('?')[0];
    let variants = [path];
    for (const part of path.matchAll(pattern)) {
      const branches = part[1].match(/^[^?]+\?\s*'([^']*)'\s*:\s*'([^']*)'$/);
      const values = branches ? [branches[1], branches[2]] : ['d0000000-0000-4000-8000-000000000000'];
      variants = variants.flatMap((variant) => values.map((value) => variant.replace(part[0], value)));
    }
    for (const variant of variants) {
      calls.push({ method: METHOD[match[1]], path: variant, where: `${relative(root, file)}:${text.slice(0, match.index).split('\n').length}` });
    }
  }
}

// ['GET', /^\/api\/v1\/me$/, …] in the demo server, read back as patterns.
const server = readFileSync(join(root, 'demo/server.ts'), 'utf8');
const routes = [...server.matchAll(/\[\s*'(GET|POST|PATCH|PUT|DELETE)',\s*\/(\^.*?\$)\/,/g)].map((match) => ({
  method: match[1],
  pattern: new RegExp(match[2]),
}));
// Uploads go through their own function in the demo server.
const uploads = [...server.matchAll(/path\.match\(\/(\^.*?\$)\/\)/g)].map((match) => new RegExp(match[1]));

if (routes.length === 0) {
  console.error('check-demo: no route found in src/demo/server.ts; has its shape changed?');
  process.exit(2);
}

const missing = calls.filter(
  ({ method, path }) =>
    !routes.some((route) => route.method === method && route.pattern.test(path)) &&
    !(method === 'POST' && uploads.some((pattern) => pattern.test(path))),
);

if (missing.length > 0) {
  console.error('The demo does not answer these calls of the client (src/demo/server.ts):\n');
  for (const call of missing) console.error(`  ${call.method} ${call.path}   (${call.where})`);
  console.error(
    '\nAdd a route for each one, with fictitious data in src/demo/data.ts so the feature shows in /demo.\nA route that cannot be faked answers with unavailable(…), which the client shows as an explained error.',
  );
  process.exit(1);
}
console.log(`check-demo: the demo answers all ${calls.length} calls the client makes.`);
