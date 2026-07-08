# OpenModelPool Agent Mobile App

> 基于 React Native 的跨平台移动客户端，支持 iOS 和 Android。

## 📱 功能概览

| 模块 | 功能 |
|------|------|
| 🏠 首页 | 快速测试 API、模型选择、快捷操作入口 |
| ⚙️ 配置 | 多 Agent 管理、添加/编辑/删除、连接测试 |
| 📊 统计 | Token 消耗统计、历史趋势图表、贡献积分 |
| 🌐 网络 | 共享网络模式切换、节点状态、试用池管理 |
| 👤 我的 | 密钥管理、分享邀请、暗黑模式切换 |

## 🚀 快速开始

### 环境要求

- Node.js >= 18
- React Native CLI 环境配置（参考 [React Native 官方文档](https://reactnative.dev/docs/environment-setup)）
- iOS: Xcode 14+ / CocoaPods
- Android: Android Studio / JDK 17 / Android SDK 34

### 安装依赖

```bash
# 安装项目依赖
cd mobile
npm install

# iOS 额外操作
cd ios && pod install && cd ..
```

### 运行

```bash
# 启动 Metro 打包器
npm start

# iOS 模拟器
npm run ios

# Android 模拟器/设备
npm run android
```

### 构建发布包

#### Android APK

```bash
# 生成 Release APK
cd android
./gradlew assembleRelease

# 产物路径：android/app/build/outputs/apk/release/app-release.apk
```

#### iOS IPA（需要 macOS + Xcode）

```bash
# 1. 打开 Xcode 工程
open ios/OpenModelPoolAgent.xcworkspace

# 2. 选择目标设备 / 配置签名
# 3. Product → Archive → Distribute App
# 4. 导出 .ipa 文件
```

## 📂 项目结构

```
mobile/
├── App.tsx                    # 入口组件，底部 Tab 导航
├── index.js                   # React Native 注册入口
├── package.json               # 依赖配置
├── tsconfig.json              # TypeScript 配置
├── babel.config.js            # Babel 配置
├── metro.config.js            # Metro 打包器配置
├── src/
│   ├── types/
│   │   └── index.ts           # TypeScript 类型定义
│   ├── components/
│   │   └── Common.tsx         # 通用组件（StatCard, ActionButton, EmptyState）
│   ├── screens/
│   │   ├── HomeScreen.tsx     # 首页：快速测试 + 模型选择
│   │   ├── ConfigScreen.tsx   # 配置：Agent 增删改查
│   │   ├── StatsScreen.tsx    # 统计：Token 消耗 + 趋势图
│   │   ├── NetworkScreen.tsx  # 网络：共享模式 + 试用池
│   │   └── ProfileScreen.tsx  # 我的：密钥管理 + 分享 + 设置
│   ├── navigation/            # 导航配置（已内聚到 App.tsx）
│   ├── services/
│   │   └── api.ts             # OpenModelPool API 封装
│   ├── store/
│   │   └── agentStore.ts      # Zustand 全局状态管理
│   ├── utils/
│   │   ├── storage.ts         # 本地持久化存储
│   │   └── theme.ts           # 主题常量（颜色/间距/字号）
│   └── assets/                # 图片、字体等静态资源
├── ios/                       # iOS 原生工程
└── android/                   # Android 原生工程
```

## 🔧 技术栈

| 技术 | 用途 |
|------|------|
| React Native 0.73 | 跨平台框架 |
| TypeScript | 类型安全 |
| React Navigation | 页面导航 |
| Zustand | 轻量状态管理 |
| React Native Paper | UI 组件库 |
| AsyncStorage | 本地数据存储 |
| react-native-encrypted-storage | 密钥加密存储（生产推荐） |
| react-native-chart-kit | 图表展示 |

## 📋 MVP 功能清单

- [x] 多 Agent 配置管理（地址 + API Key）
- [x] 切换当前 Agent
- [x] 连接测试
- [x] Chat API 测试（选择模型、输入 Prompt、查看响应、Token 消耗）
- [x] 使用统计（今日/历史/累计）
- [x] 共享网络状态查看
- [x] 密钥管理（查看/生成/删除/复制）
- [x] 分享功能
- [x] 暗黑模式切换

## 🔐 安全说明

- API Key 在本地使用 `AsyncStorage` 存储，生产环境建议替换为 `react-native-encrypted-storage` 进行加密存储
- 密钥在界面上做掩码处理，仅显示前 6 位和后 4 位
- 支持系统级剪贴板复制

## 🌍 国际化

当前版本使用中文界面。国际化支持已预留，后续可接入 `i18n-js` 或 `react-native-localize`。

## 📦 后续迭代方向

1. **流式响应（SSE）**：支持 streaming chat 实时显示
2. **推送通知**：Agent 状态变更通知
3. **生物识别**：Face ID / 指纹解锁
4. **Widget**：iOS/Android 小组件快速查看统计
5. **离线缓存**：缓存历史对话
6. **多语言**：i18n 支持

## License

MIT
