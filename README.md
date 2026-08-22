# 星匣 STARBOX

> 你的次元 · 收于一匣 — 一个轻量、开源、跑在本地的**个人次元管理**桌面应用。

用 **Go + WebView2** 写成的 Windows 桌面 App（几 MB 内存的内核 + 内嵌 HTTP 服务）。它把你的兴趣宇宙收进一匣：番剧、书库、学习、游戏、笔记、收藏、订阅、磁盘、通知、规则、情报，全部一目了然。数据只存在你本地（`data/` 目录），可完全离线运行。

**一句话**：一个「个人兴趣 ALL-IN-ONE」面板，尤其为看番 / 追番 / 二次元 / 学习党设计。

## 亮点

- **极轻内核**：只是一个调度器 + localhost HTTP 服务 + 内嵌前端（`go:embed` 打包进二进制），常驻内存仅几 MB。
- **番剧优先中文**：数据源默认走 **Bangumi 中转**（`bgmapi.anibt.net`），可选 **XinyuuDB / 萌娘百科 / AniList**。中文标题、中文制作公司、中文 CV、中文简介优先，其他语言接口（AniList）作为兜底保留。
- **多季折叠**：同一部番所有季自动归并成一张可折叠卡，默认以**第一季**为封面与主页面；点「共 N 季 ▾」**展开为 N 个独立词条**，每季各自的封面/标题/评分/进度，点进可看详情。
- **番剧详情页**：独立视图，紧凑左右两栏——制作公司、主要制作人员、同系列·其他季、简介、📝观后感/后日谈、CV 表，支持**自定义布局**（勾选显示/隐藏 + ↑↓ 排序）。
- **收藏系统**：收藏喜欢的**声优 / 制作公司**，一页看全部作品，每部作品带封面，点击即可**直接加入番剧列表**。
- **书库**：把电子书拖进去（epub/pdf/txt/md/mobi/azw3）自动识别书名/作者/格式并编目，一键在对应路径打开。
- **可视化磁盘**：treemap 树状图展示硬盘占用，可下钻、全屏。
- **本地账户**：注册 / 登录本地账户，密码用「随机盐 + SHA-256」加密存储，绝不联网上传；多账户各自独立的主题与界面布局。登录后的数据**各自隔离**（`data/users/<uid>/`），游客模式回落到共享 `data/`。注册时会自动把当前游客数据迁入新账户。
- **设置**：开机自启动（写 Windows 注册表 Run 键）、关闭主界面后「收纳到托盘 / 直接退出」，偏好只存本机。
- **通知 & 规则引擎**：聚合提醒；「如果 CPU 高 / RSS 关键词 / 追更有更新…就发通知」。
- **多主题**：暗夜 / 白天一键切换。

## 界面（左侧导航）

| 页面 | 说明 |
| --- | --- |
| 概览 | CPU / 内存 / 网络 / 磁盘环形图 + 实时指标 |
| 磁盘 | 目录占用 treemap，可下钻、全屏 |
| 订阅 | RSS / Atom 信息流（Go 标准库解析，无外部依赖） |
| 情报 | GitHub 热门、我的 CSDN、平台集成 |
| 知识库 | 番剧 / 书库 / 学习 / 游戏 / 笔记 |
| 收藏 | 声优 CV / 制作公司 + 「查看作品」 |
| 通知 | 聚合提醒中心 |
| 规则 | 如果…就… 规则引擎 |
| 设置 | 开机自启动 / 退出行为 |

## 目录结构

```
cmd/butler/             入口（-open / -desktop / -window / -tray / -config）
internal/config/        配置加载
internal/sched/         调度器 + 任务执行
internal/monitor/       各任务最新结果存储
internal/rss/           RSS 2.0 + Atom 订阅解析（Go 标准库）
internal/du/            目录容量扫描（treemap 数据源）
internal/anime/         番剧数据源：AniList / 萌娘百科 / Bangumi 中转 / XinyuuDB
internal/account/       本地账户：注册 / 登录 / 会话 / 每用户主题（密码盐+SHA-256）
internal/settings/      应用设置：开机自启动（注册表）/ 退出行为
internal/httpd/         localhost HTTP 服务 + 内嵌前端（dashboard.html）
internal/kb/            知识库存储（JSON 集合：anime/books/study/games/notes/notif/rules/connect/favs…）
internal/desktop/       WebView2 原生窗口（Windows）
internal/tray/          系统托盘
plugins/sys/            系统维护报告（Python，只读）
plugins/info/           信息收集模板（Python）
config.example.json     配置模板（真实 config.json 已被 .gitignore 排除）
```

## 构建

> 需要 Go 1.2x。若 `go` 不在 PATH，可指定绝对路径。

```powershell
go mod tidy                     # 拉取依赖
go build -o starbox.exe ./cmd/butler        # 控制台后端版
# 无控制台黑框的 GUI 版（推荐发布）：
go build -ldflags="-H=windowsgui" -o starbox.exe ./cmd/butler
```

## 运行

```powershell
.\starbox.exe -config config.json -desktop   # 启动后台服务 + 弹出原生桌面窗口（推荐）
.\starbox.exe -config config.json            # 仅后台常驻（无窗口，带托盘）
.\starbox.exe -window                        # 只弹出窗口，指向已运行的服务
```

- `-desktop`：启动后台服务 + 弹出 WebView2 原生窗口。
- `-window`：只弹窗口，连接已在运行的服务。
- `-tray`（默认开）：常驻时带系统托盘图标（右键：打开面板 / 刷新 / 退出）。
- 界面也可在浏览器打开 `http://127.0.0.1:8765/`；`/api` 返回原始 JSON，`/health` 健康检查。

## 配置（config.json）

每个任务一个对象。**务必**基于 `config.example.json` 复制为 `config.json` 再改；`config.json` 与 `data/` 已在 `.gitignore` 中排除，绝不上传。

```json
{ "id": "system_metrics", "type": "metrics", "every_seconds": 30 }
```

```json
{ "id": "rss_example", "type": "rss", "every_seconds": 3600,
  "url": "https://example.com/feed", "limit": 5, "timeout_seconds": 20 }
```

脚本写到 stdout 的内容会进入任务快照；脚本跑完即退，不占常驻内存。

## 本地 HTTP 接口（localhost）

- `GET /api` — 概览快照；`GET /health` — 健康检查
- `GET/POST/PUT/DELETE /kb/{collection}` — 知识库 CRUD（anime/books/study/games/notes/notif/rules/connect/favs/trending）
- `POST /account/register|login|logout|theme`、`GET /account/session` — 本地账户
- `GET/POST /settings` — 应用设置（开机自启动 / 退出行为）
- `POST /books/import?path=`、`POST /books/upload`、`POST /books/open?id=` — 书库
- `GET /anime/search|detail|studio|staff` — AniList
- `GET /moegirl/search|detail` — 萌娘百科
- `GET /bangumi/search|detail|persons|characters|user` — Bangumi 中转
- `GET /xinyuu/search|detail|staff-works|staff-search` — XinyuuDB
- `GET /github/trending|myrepos`、`POST /github/auth`、`GET /csdn/blog`

## 数据源说明

默认中文优先，接口可切换：

- **Bangumi 中转**（默认）：`https://bgmapi.anibt.net`，封面走 `bgmimg.anibt.net`。Chinese 标题/制作/CV 最完整。
- **XinyuuDB**：`https://db.xinyuu.cn/api`，中文动漫库（含季/开播时间）。注意其封面域名 `open.xinyuu.cn` 在部分网络不可达，App 会自动用 Bangumi/AniList 兜底取可达封面。
- **萌娘百科**：`zh.moegirl.org.cn` MediaWiki（opensearch + pageimages|extracts）。
- **AniList**：`graphql.anilist.co`，作为「其他语言」兜底接口保留。

## 安全

- `config.json`、`data/`（含 GitHub token、个人书库/笔记/账户哈希）已在 `.gitignore` 排除，**切勿强推**。
- 服务默认只监听 `127.0.0.1`，不对外暴露。

## 许可

[MIT](LICENSE)
