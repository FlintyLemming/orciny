// Orciny 下载加速 Worker
//
// 反代 GitHub Release 产物到 Cloudflare 边缘并缓存。install-agent.sh 的
// 下载 base 由 hub 注入为 https://github-dl.flinty.moe/v<version>，本 Worker 收到
// /v<version>/<file> 后原样拼到 GitHub 的 releases/download 路径上拉取，
// 命中边缘缓存则不回源。零状态、零同步：GitHub 一发 release 立刻可见，
// 没有「上传到镜像失败导致版本对不上」的故障模式（见 install.go 注释）。
//
// 部署：见 docs/operations.md。匿名即可，GitHub 公开 release 不需要凭据。

const GITHUB_OWNER = "FlintyLemming";
const GITHUB_REPO = "orciny";

// 只允许 GET/HEAD。其它方法直接拒，避免被当成开放代理。
const ALLOWED = new Set(["GET", "HEAD"]);

export default {
  async fetch(request, env, ctx) {
    const req = new URL(request.url);

    if (!ALLOWED.has(request.method)) {
      return new Response("Method Not Allowed", { status: 405 });
    }

    // 路径形如 /v0.1.0/orciny-agent_0.1.0_linux_amd64.tar.gz
    // pathname 即 /v.../file，原样追加到 GitHub 的 releases/download 之后。
    const upstream = `https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/releases/download${req.pathname}`;

    // Cache API 用原始 request 做 key（含 method）。
    const cache = caches.default;
    let resp = await cache.match(request);

    if (!resp) {
      try {
        // redirect: "follow" 是关键：GitHub 会 302 到
        // release-assets.githubusercontent.com / objects.githubusercontent.com，
        // 这些域在大陆同样慢/被干扰。让 Worker 自己跟到最终内容并缓存到边缘，
        // 客户端全程只连 github-dl.flinty.moe，才真正起到加速作用。
        const up = await fetch(upstream, {
          method: request.method,
          redirect: "follow",
        });

        if (!up.ok) {
          return new Response(`upstream ${up.status}`, { status: up.status });
        }

        resp = new Response(up.body, up);
        // tar.gz / checksums.txt 是不可变版本产物，缓存一天。release 一旦发布
        // 不会被改写，命中缓存即等价于回源。max-age 只是边缘 TTL，超时回源
        // 代价也只是一次 GitHub 拉取。
        resp.headers.set("Cache-Control", "public, max-age=86400");
        ctx.waitUntil(cache.put(request, resp.clone()));
      } catch (err) {
        return new Response(`upstream error: ${err.message}`, { status: 502 });
      }
    }

    return resp;
  },
};
