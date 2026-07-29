import PocketBase from 'pocketbase'

// 同源部署：hub 既提供 API 也提供前端，因此 baseUrl 就是当前 origin。
// 开发时由 Vite 的 proxy 转给 127.0.0.1:8090。
export const pb = new PocketBase(window.location.origin)

// realtime 订阅在开发热更新时会残留，autoCancellation 关掉可避免
// 组件重挂载时误取消仍在用的请求。
pb.autoCancellation(false)
