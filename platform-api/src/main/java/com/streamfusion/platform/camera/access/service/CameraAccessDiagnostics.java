package com.streamfusion.platform.camera.access.service;

/** Access-job recovery guidance belongs to the backend that determines the diagnostic reason. */
public final class CameraAccessDiagnostics {
    private CameraAccessDiagnostics() {}

    public static String actionHint(String code) {
        return switch (code) {
            case "AUTHENTICATION_FAILED" -> "请核对设备账户、密码以及ONVIF独立账户配置后重试";
            case "ACTOR_REVOKED" -> "当前登录或权限已失效，请重新登录并确认相机管理权限";
            case "SOURCE_CHANGED" -> "接入来源已变更，请重新读取设备目录";
            case "IMPORT_SCOPE_CHANGED" -> "视频组或账户授权已变化，已导入结果保留；请核对范围后重新确认导入";
            case "IMPORT_TIME_LIMIT_REACHED" -> "本次后台导入已到30分钟期限，已导入结果保留；可重新读取同一来源后重试";
            case "IMPORT_LIMIT_REACHED" -> "已到10000条或100页上限，已导入结果保留；同一全目录重试不会继续超过本次上限";
            case "IMPORT_ITEM_FAILED" -> "部分条目未能导入，请查看失败明细；其他已导入相机保留";
            case "DIRECTORY_CHANGED", "DIRECTORY_INCOMPLETE" ->
                    "平台目录发生变化或返回不完整，已导入结果保留；核对平台目录后可重试同一来源";
            case "IMPORT_UNAVAILABLE" -> "后台导入中断，已提交结果保留；请凭请求标识检查日志后重试";
            case "SERVER_RESTARTED", "DISCOVERY_EXPIRED" -> "请重新连接设备读取目录";
            default -> "请核对接入方式、设备地址和网络配置后重试；详情可凭请求标识定位";
        };
    }
}
