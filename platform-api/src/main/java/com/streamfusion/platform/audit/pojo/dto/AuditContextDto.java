package com.streamfusion.platform.audit.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import jakarta.servlet.http.HttpServletRequest;
import lombok.Value;

/** 审计采集已由可信代理策略解析的来源地址，以及经过清理的客户端摘要。 */
@Value
@Schema(description = "安全审计请求来源")
public class AuditContextDto {
    /** 由IP防护过滤器解析；没有可信解析结果时只使用连接地址。 */
    @Schema(description = "可信策略解析的来源IP")
    String sourceIp;

    /** 清理控制字符后的客户端摘要。 */
    @Schema(description = "客户端摘要")
    String clientSummary;

    public AuditContextDto(String sourceIp, String clientSummary) {
        this.sourceIp = clean(sourceIp, 45);
        this.clientSummary = clean(clientSummary, 255);
    }

    public static AuditContextDto from(HttpServletRequest request) {
        Object resolved = request.getAttribute("com.streamfusion.clientIp");
        return new AuditContextDto(
                resolved instanceof String ip ? ip : request.getRemoteAddr(),
                request.getHeader("User-Agent"));
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
