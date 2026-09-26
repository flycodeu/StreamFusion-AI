package com.streamfusion.platform.user.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import com.fasterxml.jackson.annotation.JsonIgnore;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.LocalDateTime;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.ToString;

/** 用户持久化实体，只用于数据库访问，不直接作为接口返回。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@TableName("sys_user")
@Schema(description = "用户持久化实体", hidden = true)
public class UserEntity {
    /** 用户主键，由后端生成雪花ID。 */
    @Schema(description = "用户ID")
    @TableId(value = "id", type = IdType.ASSIGN_ID)
    private Long id;

    /** 登录账号，大小写无关唯一。 */
    @Schema(description = "登录账号")
    private String username;

    /** 带版本和独立随机盐的单向密码哈希，不对外返回。 */
    @Schema(description = "密码哈希", hidden = true)
    @JsonIgnore
    @ToString.Exclude
    private String password;

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

    /** 账号状态：0待改密，1正常，2封禁。 */
    @Schema(description = "账号状态")
    private Integer status;

    /** 是否必须先修改密码。 */
    @Schema(description = "是否必须先修改密码")
    private Boolean mustChangePassword;

    /** 会话安全版本，安全事件递增。 */
    @Schema(description = "会话安全版本")
    private Long sessionVersion;

    /** 连续密码错误次数。 */
    @Schema(description = "连续密码错误次数")
    private Integer failedLoginCount;

    /** 登录冷却截止时间，北京时间。 */
    @Schema(description = "登录冷却截止时间")
    private LocalDateTime lockedUntil;

    /** 编辑版本，用于并发修改校验。 */
    @Schema(description = "编辑版本")
    private Long version;

    /** 创建时间，北京时间。 */
    @Schema(description = "创建时间")
    private LocalDateTime createdAt;

    /** 最后更新时间，北京时间。 */
    @Schema(description = "最后更新时间")
    private LocalDateTime updatedAt;

    /** 创建人ID。 */
    @Schema(description = "创建人ID")
    private Long createdBy;

    /** 更新人ID。 */
    @Schema(description = "更新人ID")
    private Long updatedBy;
}
