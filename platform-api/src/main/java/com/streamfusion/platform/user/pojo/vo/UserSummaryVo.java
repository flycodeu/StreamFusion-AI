package com.streamfusion.platform.user.pojo.vo;

import com.streamfusion.platform.access.pojo.vo.RoleVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 用户列表摘要。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "用户列表摘要")
public class UserSummaryVo {
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

    /** 账号状态：0待改密，1正常，2停用。 */
    @Schema(description = "账号状态")
    private Integer status;

    /** 所属部门；无主部门，空数组表示未分配。 */
    @Schema(description = "所属部门")
    private List<DepartmentVo> departments;

    /** 当前有效角色。 */
    @Schema(description = "当前有效角色")
    private List<RoleVo> roles;

    /** 编辑版本，十进制字符串。 */
    @Schema(description = "编辑版本")
    private String version;

    @Schema(description = "错误密码登录限制截止时间，与手动停用状态独立")
    private Instant lockedUntil;

    @Schema(description = "当前是否处于错误密码临时登录限制")
    private boolean loginRestricted;
}
