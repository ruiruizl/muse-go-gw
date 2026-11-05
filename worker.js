// Cloudflare Worker：把 域名/v1/* 反代到 Muse 网关（muse-go-gw）
// 用法：把本文件内容粘进 Cloudflare Worker → Deploy → 得到永久固定 URL
export default {
  async fetch(request) {
    const url = new URL(request.url);
    const UPSTREAM = "https://zhangrui2026.cc.cd";

    // 只代理 /v1 路径，其余 404
    if (!url.pathname.startsWith("/v1")) {
      return new Response("not found", { status: 404 });
    }

    const target = new URL(UPSTREAM + url.pathname + url.search);
    const headers = new Headers(request.headers);
    // 关键：Host 必须改成上游，否则 Cloudflare 隧道拒绝
    headers.set("Host", target.host);
    headers.delete("cf-connecting-ip");
    headers.delete("cf-ray");
    headers.delete("cf-visitor");
    headers.delete("cf-ipcountry");
    headers.delete("cf-worker");

    const init = {
      method: request.method,
      headers: headers,
      redirect: "manual",
    };
    if (!["GET", "HEAD"].includes(request.method)) {
      init.body = request.body;
    }

    const resp = await fetch(target.toString(), init);
    return resp;
  },
};
