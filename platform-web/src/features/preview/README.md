# 预览：从页面到播放器

`src/views/preview/Center.vue` 是菜单路由入口，持有布局、菜单、诊断和全屏等页面状态。

| 要修改的行为           | 从这里开始                                                                            | 状态归属                                                                           |
| ---------------------- | ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| 相机目录、搜索和分页   | `usePreviewCatalog.ts`                                                                | 查询结果、分页和取消。设备入口通过回调决定是否自动填充画面。                       |
| 打开收藏、按原码流重试 | `useSavedPreviewWorkspace.ts`                                                         | 收藏恢复和单槽重试。显式打开收藏时取消自动设备入口，先确认旧资源释放，再恢复位置。 |
| 播放、续租、回收、统计 | `usePreviewSlots.ts` → `player.ts` → `src/api/preview/api.ts`                         | 每个槽位的播放代次、播放器和释放结果。目录和收藏只能通过槽位操作改变播放。         |
| 节点状态和 ZLM 观测    | `PreviewNodePanel.vue` → `usePreviewNodeObservation.ts` → `PreviewNodeStatusCard.vue` | 观测结果和刷新生命周期由读取模块持有；维护、直连及 Ops 导入由面板按管理权限提供。  |

账号切换时，页面取消目录和收藏请求，清空目录，通过槽位释放旧账号的播放器。页面销毁时，各自取消读取或释放资源。

在 `platform-web` 运行：

```powershell
pnpm exec vitest run tests/views/PreviewCenterView.test.ts tests/views/PreviewNodePanel.test.ts tests/views/PreviewOpsSync.test.ts src/features/preview/usePreviewSlots.test.ts
```
