package com.streamfusion.platform.auth.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 同一Cookie会话后续写请求需要携带的CSRF信息。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "写请求所需的CSRF信息")
public class CsrfVo {
    /** 后续写请求使用的CSRF请求头名称。 */
    @Schema(description = "CSRF请求头名称")
    private String headerName;

    /** 与当前会话关联的CSRF令牌。 */
    @Schema(description = "CSRF令牌")
    private String token;
}
