package com.streamfusion.platform.auth.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.io.Serial;
import java.io.Serializable;
import java.time.Instant;
import lombok.Value;

/** Redis会话内部保存的不可变身份摘要，不对应数据库表或接口响应。 */
@Value
@Schema(description = "会话内部身份摘要")
public class SessionPrincipalDto implements Serializable {
    @Serial private static final long serialVersionUID = 1L;

    /** 已认证用户的数据库主键。 */
    @Schema(description = "已认证用户的数据库主键")
    long userId;

    /** 登录时的会话安全版本，用于识别已失效会话。 */
    @Schema(description = "登录时的会话安全版本")
    long sessionVersion;

    /** 本次成功认证时间，用于计算绝对会话期限。 */
    @Schema(description = "本次成功认证时间")
    Instant authenticatedAt;
}
