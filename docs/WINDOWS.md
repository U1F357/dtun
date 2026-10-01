# Windows 客户端（v0.2.0）

## 适配计划与范围

1. 保持 DTLS v2 应用帧、端口切换、padding、pacer 和重组协议不变，继续连接 Linux 服务端。
2. 用官方 Wintun Go binding 接入 Windows L3 网卡；将设备作为 `io.ReadWriteCloser` 交给现有隧道引擎。
3. 自动配置指定地址与 MTU，仅生成直连路由；不修改默认路由、DNS、出口 NAT。
4. 添加可关闭的接收等待、并发关闭保护和有界写入背压，确保 Ctrl+C 能停止并释放网卡。
5. 在 GitHub Windows runner 上运行原生 race 测试、真实 Wintun UDP 数据收发和关闭清理测试；发布 amd64/arm64 ZIP。

本版为命令行客户端。Windows 服务安装、GUI、自动默认路由以及 Windows 出口 NAT 不在本版范围内。ARM64 仅交叉编译，未在 ARM64 设备实测；Windows 与 Linux 的公网互联及长期运行尚需真实部署验证。

## 下载与准备

Windows 10/11 或 Windows Server，管理员权限。选择对应架构的 `dtun_windows_amd64.zip` / `dtun_windows_arm64.zip`，解压到固定目录，并保留 `dtun.exe` 旁的 `wintun.dll`。

DLL 来自 [Wintun 官方发行包](https://www.wintun.net/)，版本 0.14.1，CI 校验官方公布的 SHA-256。使用未经修改的签名 DLL，附带原许可证；dtun.exe 本身未做代码签名。

在 PowerShell 中生成身份（不需要 OpenSSL）：

```powershell
.\dtun-keygen.exe --name client --out-dir .\identity
```

`.key` 是私钥，存放在仅本人/管理员可读取的目录；Windows 权限由目录 ACL 控制。工具拒绝覆盖已有文件。通过可信途径将 `client.pin` 交给服务端，并将服务端的 `server.pin` 放入本机 `identity` 目录。

## 启动

以管理员身份打开 PowerShell，切换到解压目录。以下地址与端口都是示例，请替换：

```powershell
$serverPin = (Get-Content .\identity\server.pin -Raw).Trim()
.\dtun.exe --endpoint 198.51.100.10:20000 --ports 20000,20001,20002 `
  --switch-interval 10m --tun dtun0 --address 10.255.255.1/30 --mtu 1500 `
  --cert .\identity\client.crt --key .\identity\client.key `
  --peer-pin $serverPin --max-rate-bps 80000000
```

Linux 服务端仍使用 README 的服务端命令；`--allow-ip` 填 Windows 客户端经 NAT 后的公网 IPv4，`--peer-pin` 填客户端指纹。

另开终端：`ping 10.255.255.2`。需要访问其他网段时，按实际拓扑单独添加目标路由，并在服务端配置对应转发与回程路径。本程序不自动添加默认路由。

Ctrl+C 正常退出会结束会话并关闭临时网卡。相同名称的现有 Wintun 网卡会被拒绝，避免覆盖其他进程的配置。TUN 两端 MTU 应一致；IPv6 使用至少 1280 的 MTU，并需另行配置两端 IPv6 地址。

## 常见问题

- 找不到 DLL：确认架构匹配且 `wintun.dll` 与 exe 同目录。
- 创建网卡或设置地址失败：确认管理员权限、驱动安装策略允许，以及系统有 Windows PowerShell/NetTCPIP 命令。
- 能握手但不通：检查目标路由、对端地址、MTU和防火墙。端口列表、pacer 与 Linux 使用相同配置规则。
- UDP 出口或 NAT 改变公网 IP：同步调整服务端 `--allow-ip`。
- Wintun ring 固定 4 MiB；持续满 100ms 会返回写错误，使当前通道重连，不会无限阻塞退出。
