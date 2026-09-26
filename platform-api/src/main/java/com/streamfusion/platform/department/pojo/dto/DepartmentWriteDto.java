package com.streamfusion.platform.department.pojo.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonSetter;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "部门编辑字段")
public class DepartmentWriteDto {
    @Schema(description = "上级部门ID", nullable = true, requiredMode = Schema.RequiredMode.REQUIRED)
    private String parentId;

    @Schema(description = "部门名称", requiredMode = Schema.RequiredMode.REQUIRED)
    private String name;

    @Schema(description = "排序值", requiredMode = Schema.RequiredMode.REQUIRED)
    private Integer sortOrder;

    @Schema(hidden = true)
    @JsonIgnore
    private boolean parentIdProvided;

    @JsonSetter("parentId")
    public void setParentId(String parentId) {
        this.parentId = parentId;
        this.parentIdProvided = true;
    }
}
