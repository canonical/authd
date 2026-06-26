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

    // List the directory, following pagination so entries don't disappear.
    const prefix = path ? path.replace(/\/?$/, "/") : "";
    const objects = [];
    const delimitedPrefixes = [];
    let cursor;

    do {
      const listing = await env.BUCKET.list({ prefix, delimiter: "/", cursor });
      objects.push(...listing.objects);
      delimitedPrefixes.push(...listing.delimitedPrefixes);
      cursor = listing.truncated ? listing.cursor : undefined;
    } while (cursor);

    if (!objects.length && !delimitedPrefixes.length) {
      return new Response("Not found", { status: 404 });
    }

    // Calculate parent path for '..' link
    let parent = null;
    if (prefix) {
      const trimmed = prefix.replace(/\/$/, "");
      const idx = trimmed.lastIndexOf("/");
      parent = idx === -1 ? "" : trimmed.slice(0, idx + 1);
    }

    // Show a 'log' shortcut only on directories that actually contain a
    // log.html, so it doesn't appear on intermediate dirs where it 404s.
    const sortedPrefixes = [...new Set(delimitedPrefixes)].sort();
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
