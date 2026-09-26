package com.streamfusion.platform.user.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 当前用户个人资料。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "当前用户个人资料")
public class UserProfileVo {
    /** 用户ID，十进制字符串。 */
    @Schema(description = "用户ID")
    private String id;

    /** 登录账号，4至32位英文字母和数字。 */
    @Schema(description = "登录账号")
    private String username;

    /** 昵称。 */
    @Schema(description = "昵称")
    private String nickname;

    /** 头像标识。 */
    @Schema(description = "头像标识")
    private String avatarKey;

    /** 联系电话。 */
    @Schema(description = "联系电话")
    private String phone;

    /** 联系邮箱。 */
    @Schema(description = "联系邮箱")
    private String email;

    /** 性别：0未设置，1男，2女。 */
    @Schema(description = "性别")
    private Integer gender;

    /** 账号状态：0待改密，1正常，2停用。 */
    @Schema(description = "账号状态")
    private Integer status;

    /** 是否必须先修改密码。 */
    @Schema(description = "是否必须先修改密码")
    private Boolean mustChangePassword;

    /** 编辑版本，十进制字符串。 */
    @Schema(description = "编辑版本")
    private String version;
}
