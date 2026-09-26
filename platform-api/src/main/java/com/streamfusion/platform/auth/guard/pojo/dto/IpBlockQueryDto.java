package com.streamfusion.platform.auth.guard.pojo.dto;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "IP封禁分页查询")
public class IpBlockQueryDto extends PageQueryDto {
    @Schema(description = "精确来源IP，可选IPv4或IPv6")
    private String sourceIp;

    @Schema(description = "状态筛选：BLOCKED、RELEASED")
    private String status;
}
