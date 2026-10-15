package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.pojo.vo.CameraAddressVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;

@Schema(description = "按当前授权通道分页的控制层状态汇总，无网络检查")
public record CameraStatusPageVo(
        @Schema(description = "当前页通道列表") List<Item> items,
        @Schema(description = "当前权限及筛选下的通道总数") long total,
        @Schema(description = "当前页码，从1开始") int page,
        @Schema(description = "每页通道数，1至100") int size,
        @Schema(description = "本次读取的服务器UTC时间，不续期观察事实") Instant serverNow,
        @Schema(description = "当前页代表码流的排他计数，不是设备在线率或全量统计") Summary summary) {
    @Schema(name = "CameraStatusPageSummaryVo", description = "仅统计当前页各通道的代表码流")
    public record Summary(
            @Schema(description = "当前页通道数") int total,
            @Schema(description = "代表码流具有有效连接可达事实的通道数") int accessible,
            @Schema(description = "代表码流具有有效连接失败事实的通道数") int failed,
            @Schema(description = "代表码流检查等待或执行中的通道数") int checking,
            @Schema(description = "代表码流事实过期或配置已变化的通道数") int stale,
            @Schema(description = "尚无可用代表码流事实的启用通道数") int unknown,
            @Schema(description = "未启用的通道数，包含停用及待归档") int disabled) {}

    @Schema(name = "CameraStatusPageItemVo", description = "通道及其码流状态，禁用与观察事实分别展示")
    public record Item(
            @Schema(description = "稳定通道ID") String cameraId,
            @Schema(description = "设备组键：d+设备ID，未关联设备为c+通道ID") String groupKey,
            @Schema(description = "通道名称") String name,
            @Schema(description = "可靠设备名称，未关联或未知为空", nullable = true) String deviceName,
            @Schema(description = "授权通道的接入地址；平台地址明确标识", nullable = true)
                    CameraAddressVo accessAddress,
            @Schema(description = "期望生命周期，不等于在线状态") String lifecycle,
            @Schema(description = "默认预览码流ID，代表流优先选取启用的默认码流", nullable = true)
                    String defaultPreviewProfileId,
            @Schema(description = "当前通道全部Profile，允许为空") List<Profile> profiles) {}

    @Schema(name = "CameraStatusPageProfileVo", description = "码流档案与CONTROL事实；不代表媒体可解码")
    public record Profile(
            @Schema(description = "稳定码流ID") String streamProfileId,
            @Schema(description = "本地码流标签，未知为空", nullable = true) String name,
            @Schema(description = "声明的码流用途MAIN/SUB/THIRD/CUSTOM/UNKNOWN，未知可空", nullable = true)
                    String streamKind,
            @Schema(description = "是否允许新使用") boolean enabled,
            @Schema(description = "固定CONTROL") String layer,
            @Schema(description = "固定RTSP_DESCRIBE") String probeKind,
            @Schema(description = "上次合法事实，未检查为空", nullable = true)
                    CameraStatusObservationVo observation,
            @Schema(description = "NEVER/CURRENT/STALE") String freshness,
            @Schema(description = "事实是否仍匹配当前实际连接语义") boolean configurationCurrent,
            @Schema(description = "本Profile最新保留检查回执，允许为空", nullable = true)
                    CameraStatusCheckVo latestCheck,
            @Schema(description = "当前配置是否允许新显式控制检查") boolean canCheck,
            @Schema(description = "阻止检查的受控原因，可检查为空", nullable = true) String blockingReasonCode,
            @Schema(description = "连接采样时间；配置变化或无事实时为空", nullable = true)
                    CameraStatusTimelineVo timeline) {}
}
