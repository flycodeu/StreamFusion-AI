package com.streamfusion.platform.audit.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import java.util.Map;

public record AuditDetailVo(
        @Schema(description = "操作记录基础信息") AuditEntryVo record,
        @Schema(description = "请求来源 IP") String sourceIp,
        @Schema(description = "经过白名单过滤的有界变更摘要，不包含凭据") Map<String, Object> changes,
        @Schema(description = "关系字段对应的可读对象列表及名称来源，键与changes原ID字段一致")
                Map<String, List<AuditReferenceVo>> relations) {}
