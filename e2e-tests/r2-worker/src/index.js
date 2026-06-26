// List a directory, following pagination so entries don't disappear.
async function listDir(bucket, prefix) {
  const objects = [];
  const delimitedPrefixes = [];
  let cursor;

  do {
    const listing = await bucket.list({ prefix, delimiter: "/", cursor });
    objects.push(...listing.objects);
    delimitedPrefixes.push(...listing.delimitedPrefixes);
    cursor = listing.truncated ? listing.cursor : undefined;
  } while (cursor);

  return { objects, uniquePrefixes: [...new Set(delimitedPrefixes)] };
}

// A directory collapses (redirects straight through) when it holds nothing
// but a single subdirectory, so there's no real choice to make.
function collapsesTo({ objects, uniquePrefixes }) {
  return !objects.length && uniquePrefixes.length === 1 ? uniquePrefixes[0] : null;
}

// Immediate parent of a directory prefix; "" for a top-level dir, null at root.
function parentOf(prefix) {
  if (!prefix) return null;
  const trimmed = prefix.replace(/\/$/, "");
  const idx = trimmed.lastIndexOf("/");
  return idx === -1 ? "" : trimmed.slice(0, idx + 1);
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const path = decodeURIComponent(url.pathname.replace(/^\//, ""));
    const wantsDirectory = url.pathname.endsWith("/");

    // Serve file objects only for non-directory paths.
    if (!wantsDirectory && path) {
      const object = await env.BUCKET.get(path);
      if (object) {
        const headers = new Headers();
        object.writeHttpMetadata(headers);
        return new Response(object.body, { headers });
      }
    }

    const prefix = path ? path.replace(/\/?$/, "/") : "";
    const { objects, uniquePrefixes } = await listDir(env.BUCKET, prefix);

    if (!objects.length && !uniquePrefixes.length) {
      return new Response("Not found", { status: 404 });
    }

    // Redirect straight through collapsing directories instead of showing a
    // dead-end listing with a single entry.
    const collapseTarget = collapsesTo({ objects, uniquePrefixes });
    if (wantsDirectory && collapseTarget) {
      return Response.redirect(new URL(`/${collapseTarget}`, url), 302);
    }

    // Compute the '..' target, walking up past ancestors that would only
    // redirect back down here, so '..' lands somewhere with a real choice.
    let parent = parentOf(prefix);
    while (parent) {
      const grandparent = parentOf(parent);
      if (collapsesTo(await listDir(env.BUCKET, parent)) === null) break;
      parent = grandparent;
    }

    // Show a 'log' shortcut only on directories that actually contain a
    // log.html, so it doesn't appear on intermediate dirs where it 404s.
    const sortedPrefixes = [...uniquePrefixes].sort();
    const hasLog = await Promise.all(
      sortedPrefixes.map(p => env.BUCKET.head(`${p}log.html`).then(o => o !== null))
    );

    const rows = [
      // Add '..' row if not at root
      ...(parent !== null ? [`<li><a href="/${parent}">..</a></li>`] : []),
      ...sortedPrefixes.map((p, i) => {
        const name = p.replace(prefix, "");
        const log = hasLog[i] ? ` [<a href="/${p}log.html">log</a>]` : "";
        return `<li><a href="/${p}">${name}</a>${log}</li>`;
      }),
      ...objects.map(o => {
        const name = o.key.replace(prefix, "");
        return `<li><a href="/${o.key}">${name}</a></li>`;
      }),
    ].join("\n");

    return new Response(
      `<!DOCTYPE html><html><body><h1>/${prefix}</h1><ul>${rows}</ul></body></html>`,
      { headers: { "content-type": "text/html" } }
    );
  },
};
