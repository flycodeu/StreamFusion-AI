package com.streamfusion.platform.audit.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

public record AuditReferenceVo(
        @Schema(description = "对象历史ID，十进制字符串") String id,
        @Schema(description = "对象类型：USER、ROLE、DEPT、MENU、IP_BLOCK等") String type,
        @Schema(description = "对象名称，无法取得时为空") String name,
        @Schema(description = "账号或业务编码，不适用或无法取得时为空") String code,
        @Schema(description = "名称来源：SNAPSHOT操作时快照、CURRENT当前名称、MISSING已删除或未知") String source) {}
