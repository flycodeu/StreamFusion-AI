package com.streamfusion.platform.common.pojo.dto;

import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 公共分页入参；业务查询 DTO 继承后仅增加自己的筛选条件。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "公共分页请求")
public class PageQueryDto {
    public static final int MAX_SIZE = 100;

    /** 页码从1开始，不允许通过负数关闭分页。 */
    @Schema(
            description = "页码",
            defaultValue = "1",
            minimum = "1",
            nullable = false,
            requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private int page = 1;

    /** 每页记录数，默认20，上限100。 */
    @Schema(
            description = "每页记录数",
            defaultValue = "20",
            minimum = "1",
            maximum = "100",
            nullable = false,
            requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private int size = 20;

    public final <T> Page<T> toPage() {
        if (page < 1) throw invalidField("page");
        if (size < 1 || size > MAX_SIZE) throw invalidField("size");
        return new Page<>(page, size);
    }

    protected static BusinessException invalidField(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
