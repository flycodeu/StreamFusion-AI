package com.streamfusion.platform.camera.access.pojo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "最多128个IPv4地址与4个端口的TCP候选探测，不识别相机或读取媒体")
public record CameraScanRequest(
        @Schema(description = "带时间戳的幂等请求标识") String clientRequestId,
        @Schema(description = "部署批准的网络策略键") String networkPolicyKey,
        @Schema(description = "IPv4 CIDR，与起止地址互斥") String cidr,
        @Schema(description = "IPv4起始地址，包含端点") String startAddress,
        @Schema(description = "IPv4结束地址，包含端点") String endAddress,
        @Schema(description = "1到4个不同TCP端口") List<Integer> ports) {}
