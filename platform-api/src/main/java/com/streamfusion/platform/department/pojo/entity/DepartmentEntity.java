package com.streamfusion.platform.department.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** 公司、部门和组使用同一种树节点。 */
@Getter
@Setter
@TableName("sys_dept")
@Schema(description = "部门数据", hidden = true)
public class DepartmentEntity {
    @TableId(value = "id", type = IdType.ASSIGN_ID)
    @Schema(description = "部门ID")
    private Long id;

    @Schema(description = "上级部门ID")
    private Long parentId;

    @Schema(description = "部门名称")
    private String name;

    @Schema(description = "排序值")
    private Integer sortOrder;

    @Schema(description = "编辑版本")
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
