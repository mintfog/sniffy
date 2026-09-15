# 出站 TLS 证书校验

Sniffy 默认校验出站 HTTPS / WSS 的证书信任链、主机名和有效期。源站证书不受信任时停止转发,不会通过其他转发路径绕过校验。

客户端信任 Sniffy CA,只说明客户端接受 Sniffy 生成的代理证书,不代表 Sniffy 已信任源站证书。内部测试环境优先把源站的专用 CA 导入 Sniffy 所在系统的可信根证书库,并重启 Sniffy,不要直接关闭校验。

仅在确有调试需求时配置 `tlsInsecureHosts`,明确承担这些主机遭源站冒充的风险。名单默认为空;元素只能是精确域名或 IP,不含端口、URL、通配符或首尾空白。不匹配子域名。域名不区分大小写,可带一个末尾点;保存时统一规范化、去重和排序。

在用户配置目录的 `config.json` 中加入字段,保留其他已有配置:

```json
{
  "tlsInsecureHosts": ["dev.example.test", "127.0.0.1"]
}
```

headless 管理 API 通过 `PUT` 或 `POST /api/config` 合并配置补丁,请求需携带 Bearer token。以下命令中的 `SNIFFY_API_TOKEN` 应设为 Sniffy 使用的管理 API token,对应启动环境变量或配置区 `api_token` 文件中的值。

运行时设置例外:

```sh
curl -X PUT http://127.0.0.1:8888/api/config \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"tlsInsecureHosts":["dev.example.test"]}'
```

撤销全部例外:

```sh
curl -X PUT http://127.0.0.1:8888/api/config \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"tlsInsecureHosts":[]}'
```

API 非法名单不会替换当前名单;磁盘加载非法名单时清空例外。修改或撤销对随后发起的请求和连接生效,不强制中断已经开始的请求。已建立的长连接(如 WSS)需断开重连。未解密的 CONNECT 隧道仍由客户端直接验证源站证书。
