package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.pojo.vo.CameraAddressVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;

@Schema(description = "授权范围内的分层状态分页，无网络检查；旧入口按通道，devices入口按设备的一路代表通道")
public record CameraStatusPageVo(
        @Schema(description = "当前页条目；设备入口每个设备仅返回一路代表通道") List<Item> items,
        @Schema(description = "当前权限及筛选下的条目总数，按入口分别为通道数或设备数") long total,
        @Schema(description = "当前页码，从1开始") int page,
        @Schema(description = "每页条目数，1至100") int size,
        @Schema(description = "本次读取的服务器UTC时间，不续期观察事实") Instant serverNow,
        @Schema(description = "当前页代表码流的排他计数，不是设备在线率或全量统计") Summary summary) {
    @Schema(name = "CameraStatusPageSummaryVo", description = "仅统计当前页条目的代表码流；设备入口不逐通道求和")
    public record Summary(
            @Schema(description = "当前页条目数") int total,
            @Schema(description = "代表码流所选层具有有效通过事实的条目数") int accessible,
            @Schema(description = "代表码流所选层具有有效失败事实的条目数") int failed,
            @Schema(description = "代表码流检查等待或执行中的条目数") int checking,
            @Schema(description = "代表码流最近结果与当前连接配置不匹配的条目数") int stale,
            @Schema(description = "尚无可用代表码流事实的启用条目数") int unknown,
            @Schema(description = "未启用的条目数；设备入口仅在没有启用代表通道时计入") int disabled) {}

    @Schema(name = "CameraStatusPageItemVo", description = "通道及其码流状态，禁用与观察事实分别展示")
    public record Item(
            @Schema(description = "稳定通道ID") String cameraId,
            @Schema(description = "设备组键：d+设备ID，未关联设备为c+通道ID") String groupKey,
            @Schema(description = "通道名称") String name,
            @Schema(description = "可靠设备名称，未关联或未知为空", nullable = true) String deviceName,
            @Schema(description = "授权通道的接入地址；平台地址明确标识", nullable = true)
                    CameraAddressVo accessAddress,
            @Schema(description = "期望生命周期，不等于在线状态") String lifecycle,
            @Schema(description = "默认预览码流ID，仅用于预览，不决定检测代表流", nullable = true)
                    String defaultPreviewProfileId,
            @Schema(description = "当前通道全部Profile，按低负载检测顺序返回：已知正码率优先升序，再按有效面积、用途和ID回退；允许为空")
                    List<Profile> profiles) {}

    @Schema(name = "CameraStatusPageProfileVo", description = "码流档案与连接/收流事实")
    public record Profile(
            @Schema(description = "稳定码流ID") String streamProfileId,
            @Schema(description = "本地码流标签，未知为空", nullable = true) String name,
            @Schema(description = "声明的码流用途MAIN/SUB/THIRD/CUSTOM/UNKNOWN，未知可空", nullable = true)
                    String streamKind,
            @Schema(description = "是否允许新使用") boolean enabled,
            @Schema(description = "RTSP控制连接检查") CameraStatusLayerVo control,
            @Schema(description = "媒体接收检查，不证明解码或画面正常") CameraStatusLayerVo media,
            @Schema(description = "平台官方在线状态") CameraStatusLayerVo upstream) {}
}
