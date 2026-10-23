# Harness正常接续入口核对

2026-10-24：原生getAXState再次报告Mac锁定。127.0.0.1:60406仍由DeepSeek PID7563监听；IAB打开该服务后显示“dsh web authentication required; reopen the URL printed by dsh web.”，未取得会话访问。

找到应用自带命令：`/Applications/DeepSeek Harness.app/Contents/Resources/runtime/cli/bin/dsh`。`--help` exit0；`--profile desktop --help` exit1，明确提示desktop由Electron应用独占管理。没有启动新profile、复制认证或改用独立OpenCode。Harness原会话返工仍待原生桌面解锁。

本地Nerae预览的IAB接口另行核实可用，因此继续不依赖原生窗口的首批验收；不能把原生工具受阻扩大成全部浏览器验收都不可做。
