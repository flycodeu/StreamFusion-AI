package com.streamfusion.platform.user.pojo.dto;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.EqualsAndHashCode;
import lombok.NoArgsConstructor;

/** 用户分页查询，分页规则由公共请求对象维护。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@EqualsAndHashCode(callSuper = true)
@Schema(description = "用户分页查询")
public class UserQueryDto extends PageQueryDto {
    /** 账号或昵称关键词，最多64个字符，通配符按字面查询。 */
    @Schema(
            description = "账号或昵称关键词",
            maxLength = 64,
            nullable = true,
            requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private String keyword;

    /** 账号状态筛选：0待改密，1正常，2封禁；空表示全部。 */
    @Schema(
            description = "账号状态筛选",
            allowableValues = {"0", "1", "2"},
            nullable = true,
            requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private Integer status;

    public void validateFilters() {
        if (keyword != null && keyword.codePointCount(0, keyword.length()) > 64)
            throw invalidField("keyword");
        if (status != null && !UserStatus.isValid(status)) throw invalidField("status");
    }
}
