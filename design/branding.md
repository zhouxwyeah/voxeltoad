# Branding — voxeltoad

> 项目品牌命名与视觉资产的单一事实来源。任何对外暴露的文案 / logo / 标题 / 包名选用，先对齐本文。

## 1. 品牌名

**权威写法：`voxeltoad`**（全小写、9 字符、无分隔符）。

| 场景 | 写法 |
| --- | --- |
| 正文 / 包名 / 域名 / 镜像名 / go module | `voxeltoad` |
| 标题句首 / Banner logotype | `Voxeltoad` |
| 中文语境 | 「voxeltoad」或「Voxeltoad 网关」 |
| **禁止** | `VoxelToad`、`voxel-toad`、`voxel_toad`、`VT` |

### 命名由来

- **voxel**（体素）：Minecraft 世界的基本构建单元，呼应"网关是模型请求的拼装/分发层"。
- **toad**：马里奥蘑菇王国的信使与道具分发员，呼应"网关为请求指路、为调用方发密钥/发卡"。

双 IP 同时命中，再造词远离 Nintendo / Mojang 注册商标本体。

### 工程可用性

| 用途 | 形式 | 状态 |
| --- | --- | --- |
| go module | `module voxeltoad` | ✅ 合法 |
| npm scope | `@voxeltoad/gateway-sdk` | ✅ 合法 |
| docker image | `voxeltoad/gateway` / `voxeltoad/admin` | ✅ 合法 |
| helm release | `voxeltoad` | ✅ 合法 |
| bundle id（反写） | `dev.voxeltoad.desktop` | ✅ 合法 |
| 域名 | `voxeltoad.dev` / `.io` / `.com` | 待采购 |

## 2. Logo

### 造型

像素蘑菇头（voxel toad 的伞）+ 竖直柄（象征"通道/管道"），柄中央镂空 1px 通道，暗喻"转发"。

- 伞面按 **4×4 像素块**网格走轮廓。
- 伞斑使用负形（背景色）+ 高光色点缀。
- 柄部 1×3 像素柱，与伞面中线对齐。

### 配色

| 角色 | 色值 | 用途 |
| --- | --- | --- |
| 主色（伞面/柄） | `#2456f6` | 与 design-system 品牌蓝一致，**不新增 token** |
| 高光（伞面 1-2 像素点） | `#7ee0ff` | MC 钻石青，仅作点缀 |
| 负形（伞斑/通道镂空） | `#ffffff` 或背景色 | 透明负形 |
| 装饰星（可选，仅 README 横幅） | `#ffd23f` | 不进入 favicon 与产品 UI |
| Dock 底色 | `#0b1533` | 圆角深底 |

### 适配矩阵

| 场景 | 尺寸 | 文件 | 备注 |
| --- | --- | --- | --- |
| favicon | 16×16 | `web/public/logo-mark.svg` | 简化版，去箭头细节 |
| 控制台 header | 32×32 | `web/public/logo.svg` | 完整版 |
| README 顶部 | 横排横幅 | `web/public/logo.svg` + 文字 | 中文副标「企业级大模型网关」 |
| 桌面 dock | 1024×1024 | 等距立体版（待补） | 圆角 `#0b1533` 深底 |

### 资产清单

- `web/public/logo.svg` — 主 logo，扁平像素版。
- `web/public/logo-mark.svg` — favicon/小尺寸简化版。
- 等距立体版（dock 用）→ 待桌面端重命名 PR 中补齐。

## 3. 候选名归档

| 候选 | 构成 | 未选原因 |
| --- | --- | --- |
| `cubefox` | cube + fox | cubefox.com 域名有占用风险 |
| `pixelyak` | pixel + yak | 儿童亲和力弱于 toad |
| `blockmole` | block + mole | 工程感强、可爱感弱 |
| `endercat` | ender + cat | 距 Mojang "Ender" 商标过近，企业产品避用 |

## 4. 迁移说明

- 工程重命名（go module / helm / npm scope / desktop bundle id / 前端 title / 文档全文）按规划 PR 单独推进，不在本文范围。
- 本文只约束**新品牌的语义与视觉规则**；具体替换步骤见当次重命名 PR 的 plan。
