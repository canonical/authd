export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const path = decodeURIComponent(url.pathname.replace(/^\//, ""));

    // Try to serve the file directly
    const object = await env.BUCKET.get(path);
    if (object) {
      const headers = new Headers();
      object.writeHttpMetadata(headers);
      return new Response(object.body, { headers });
    }

    // List the directory
    const prefix = path ? path.replace(/\/?$/, "/") : "";
    const listing = await env.BUCKET.list({ prefix, delimiter: "/" });

    if (!listing.objects.length && !listing.delimitedPrefixes.length) {
      return new Response("Not found", { status: 404 });
    }

    // Calculate parent path for '..' link
    let parent = null;
    if (prefix) {
      const trimmed = prefix.replace(/\/$/, "");
      const idx = trimmed.lastIndexOf("/");
      parent = idx === -1 ? "" : trimmed.slice(0, idx + 1);
    }

    const rows = [
      // Add '..' row if not at root
      ...(parent !== null ? [`<li><a href="/${parent}">..</a></li>`] : []),
      ...listing.delimitedPrefixes.map(p => {
        const name = p.replace(prefix, "");
        return `<li><a href="/${p}">${name}</a></li>`;
      }),
      ...listing.objects.map(o => {
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
