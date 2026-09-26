package com.streamfusion.platform.role.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** 角色持久化数据，仅供后端内部访问。 */
@Getter
@Setter
@TableName("sys_role")
@Schema(description = "角色数据", hidden = true)
public class RoleEntity {
    /** 角色主键。 */
    @TableId(type = IdType.ASSIGN_ID)
    @Schema(description = "角色ID")
    private Long id;

    /** 角色编码，SUPER_ADMIN 表示内置超级管理员。 */
    @Schema(description = "角色编码")
    private String code;

    /** 角色显示名称。 */
    @Schema(description = "角色名称")
    private String name;

    /** 角色状态：ENABLED 启用，DISABLED 停用。 */
    @Schema(description = "角色状态")
    private String status;

    @Schema(description = "角色说明")
    private String description;

    @Schema(description = "并发标记")
    private Long version;

    @Schema(description = "创建时间")
    private LocalDateTime createdAt;

    @Schema(description = "更新时间")
    private LocalDateTime updatedAt;

    @Schema(description = "创建人ID")
    private Long createdBy;

    @Schema(description = "更新人ID")
    private Long updatedBy;
}
