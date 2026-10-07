package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonSetter;
import com.fasterxml.jackson.annotation.Nulls;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "视频分组创建或编辑命令")
public class CameraGroupWriteDto {
    @Schema(description = "编辑版本，创建时不提交")
    private String version;

    @Schema(description = "分组名称，最多64个Unicode码点")
    @JsonSetter(nulls = Nulls.FAIL)
    private String name;

    @Schema(description = "非负排序值，创建缺省为0")
    @JsonSetter(nulls = Nulls.FAIL)
    private Integer sortOrder;

    @Schema(description = "移组预览返回的五分钟有效确认凭据")
    private String confirmation;

    @Schema(description = "上级视频组ID，显式null表示根组，编辑省略表示不修改")
    private String parentId;

    @Schema(description = "备注，最多500个Unicode码点，显式null清空")
    private String remark;

    @JsonIgnore
    @Schema(hidden = true)
    private boolean parentIdProvided;

    @JsonIgnore
    @Schema(hidden = true)
    private boolean remarkProvided;

    public void setParentId(String value) {
        parentId = value;
        parentIdProvided = true;
    }

    public void setRemark(String value) {
        remark = value;
        remarkProvided = true;
    }
}
