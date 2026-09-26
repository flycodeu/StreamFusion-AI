package com.streamfusion.platform.audit.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import jakarta.servlet.http.HttpServletRequest;
import lombok.Value;

/** 审计只采集连接地址和经过清理的客户端摘要。 */
@Value
@Schema(description = "安全审计请求来源")
public class AuditContextDto {
    /** 连接来源 IP，不读取转发请求头。 */
    @Schema(description = "连接来源 IP")
    String sourceIp;

    /** 清理控制字符后的客户端摘要。 */
    @Schema(description = "客户端摘要")
    String clientSummary;

    public AuditContextDto(String sourceIp, String clientSummary) {
        this.sourceIp = clean(sourceIp, 45);
        this.clientSummary = clean(clientSummary, 255);
    }

    public static AuditContextDto from(HttpServletRequest request) {
        return new AuditContextDto(request.getRemoteAddr(), request.getHeader("User-Agent"));
    }

    private static String clean(String value, int maxCodePoints) {
        if (value == null) {
            return null;
        }
        StringBuilder clean = new StringBuilder();
        value.codePoints()
                .filter(codePoint -> !Character.isISOControl(codePoint))
                .limit(maxCodePoints)
                .forEach(clean::appendCodePoint);
        String result = clean.toString().strip();
        return result.isEmpty() ? null : result;
    }
}
