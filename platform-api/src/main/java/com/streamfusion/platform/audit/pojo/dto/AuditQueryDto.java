package com.streamfusion.platform.audit.pojo.dto;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "操作记录查询")
public class AuditQueryDto extends PageQueryDto {
    @Schema(description = "操作者ID、当前账号或昵称")
    private String user;

    private String action;

    @Schema(description = "目标模块，例如USER、ROLE、MENU、DEPT")
    private String module;

    private String result;

    @Schema(description = "精确请求追踪标识，32位十六进制，大小写输入均可")
    private String traceId;

    @Schema(description = "起始时间，UTC Instant")
    private String startTime;

    @Schema(description = "截止时间，UTC Instant")
    private String endTime;
}
