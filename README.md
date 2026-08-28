# 星匣 STARBOX

> 你的次元 · 收于一匣 — 轻量、开源、跑在本地的个人次元管理桌面应用。

**Go + 原生 Win32（GDI owner-draw）**，零第三方 UI 依赖，单文件约 10 MB，无控制台窗口。番剧、书库、学习、游戏、笔记、收藏、订阅、磁盘、通知、情报，全部收进一匣。数据只存本地 `data/` 目录。

**名称**：正式名「星匣 STARBOX」，谐音「星河」——把兴趣宇宙里的一颗颗「星」收进一只「匣子」。

## 功能现状

| 页面 | 状态 | 说明 |
| --- | --- | --- |
| 概览 | ✅ | CPU / 内存 / 运行时长 / 磁盘 四卡片，5 秒自动刷新；高分屏自适应（Per-Monitor V2） |
| 磁盘 | ✅ | 分区用量 + C 盘顶层目录占用扫描（后台异步） |
| 订阅 | ✅ | RSS / Atom，源在 `config.json` 的 tasks 里配置 |
| 情报 | ✅ | GitHub Trending + 平台绑定（GitHub 凭据走 API 验证，token DPAPI 加密保存）+ 我的仓库 |
| 知识库 | ✅ | 番剧卡片墙（中文优先搜索 / 封面保持纵横比 / 详情 / 状态 / 看一集+1）/ 书库 / 学习 / 游戏 / 笔记（可管理）；番剧详情展示制作公司与声优，★ 一键收藏 |
| 收藏 | ✅ | 声优 / 制作公司收藏，查看作品网格（依赖 AniList ID） |
| 通知 | ✅ | 追更提醒（追踪番剧未来 7 天播出表）+ 订阅更新，自动去重，点击打开对应页面 |
| 规则 | 🚧 | 预留 |
| 设置 | 🔶 | 开机自启动（注册表 HKCU Run）；托盘 / 退出行为待接入 |

## 构建

需要 Go 1.22+（项目自带 `.tools/gosdk` 则无需安装）：

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1
```

产物：`star.exe`（主程序）、`setup.exe`（安装器，内嵌主程序与卸载器）、`unins.exe`（卸载器）。

也可以手动构建：

```powershell
go build -ldflags "-H=windowsgui" -o star.exe .\cmd\star
```

## 目录结构

```
cmd/star/     主程序（原生 Win32 UI）
cmd/setup/    安装器（go:embed 内嵌 starbox.exe + unins.exe）
cmd/unin/     独立卸载器（可选保留 data/ 用户数据）
internal/anime/     番剧数据源：AniList / Bangumi 中转 / XinyuuDB / 萌娘百科
internal/kb/        JSON 集合存储（data/*.json）
internal/rss/       RSS 2.0 + Atom 解析（Go 标准库）
internal/githot/    GitHub Trending / 用户仓库 / 凭据验证
internal/du/        目录容量扫描
internal/settings/  设置 + 自启动注册表 + DPAPI 凭据保护
build.ps1     一键构建脚本
```

## 数据与隐私

- 所有数据存储在安装目录的 `data/` 下（JSON 文件 + 封面缓存），卸载时可勾选保留
- GitHub token 用 Windows DPAPI（当前用户）加密后落盘，其他平台凭据暂为明文（升级计划中）
- 追更提醒依赖 AniList GraphQL API；番剧中文元数据依赖 Bangumi 中转（bgmapi.anibt.net）

## License

见 [LICENSE](LICENSE)。

