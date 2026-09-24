# Web UI

原生 Go 服务内置了一个无依赖的浏览器工作台。它使用同源 HTTP API，不需要
Node 构建、单独的前端服务、CDN 或模型。

## 启动

```bash
aiwr serve
```

打开 [http://127.0.0.1:8765/](http://127.0.0.1:8765/)。当前 UI 可以：

- 粘贴文本或选择一个本地文件；
- 调用 `/inspect`、`/detect` 和 `/clean`；
- 显示 JSON 报告以及 `/capabilities` 报告的可选 backend；
- 预览文本清理结果并下载新的输出文件。

默认执行 `aiwr serve` 不需要 API key，并且只监听本机
（`127.0.0.1`）。

浏览器把文件字节以 base64 发送到当前 aiwr 服务。默认选中
`layer_a_only`，所以 UI 不会调用重写模型或远程 backend；除非后续 API 请求
显式加入这类选项。清理操作只准备下载文件，不会在浏览器中覆盖原始输入。

## 鉴权

只有在明确要把 API 暴露给本机以外的用户时，才设置 bearer key：

```bash
WATERMARKS_SERVER_API_KEY=change-me aiwr serve
```

`/` 及其静态资源仍然可访问，但 API 请求会受保护。这个简化 UI 面向默认的
本机无 key 模式；启用鉴权后，请使用 API 客户端直接发送
`Authorization: Bearer ...`。aiwr 不会把 key 放入 URL。

服务默认只监听 `127.0.0.1:8765`。如果要绑定或反向代理到其它主机，请同时
配置鉴权、TLS 和文件访问策略。UI 不接受任意 API base URL，保持同源可以避免
它变成访问其它服务的通用代理。

## API 和限制

UI 是方便的客户端，不替代机器可读的 API 契约。客户端生成请使用
[`/openapi.json`](http://127.0.0.1:8765/openapi.json)，请求限制和选项语义见
[用户手册](USER_GUIDE.zh-CN.md)。目录、批处理、Layer B strategy、原地修改、
SARIF 或可复现自动化请使用 CLI 或直接调用 API。

UI 展示的是当前服务实际报告的能力；打开页面不会安装缺失的 scorer 或可选工具。
清理仍然是 best-effort，不能证明人类创作，也不能保证私有、密钥型、像素域或
波形信号已经消失。
