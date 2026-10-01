# 更新记录

## v0.2.0 — Windows 客户端

- 新增 Wintun 后端：网卡创建、IP/MTU 配置、L3 包收发和退出清理。
- 复用 DTLS、多端口软切换、随机 padding、pacer 与重组协议，与现有 Linux 服务端保持协议兼容。
- 新增跨平台 `dtun-keygen`，生成证书、私钥和 SPKI 指纹，无需 OpenSSL。
- GitHub Actions 增加 Windows 原生 race/vet 检查和真实 Wintun UDP 往返测试，检查默认路由及网卡清理。
- 发布 Linux / Windows amd64 与 arm64 包；Windows 包携带官方签名 Wintun DLL 和许可证。
- 不自动配置默认路由或 DNS。Windows 服务安装、GUI、出口 NAT 暂未提供。
- ARM64 仅交叉编译；Windows 公网长时间运行及 Windows/Linux 实际网络互通尚需部署验证。

## v0.1.0

首次发布 Linux TUN + DTLS/UDP 实验实现。
