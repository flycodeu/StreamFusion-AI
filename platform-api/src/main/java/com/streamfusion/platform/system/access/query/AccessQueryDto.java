package com.streamfusion.platform.system.access.query;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "只读权限查询筛选，不修改授权")
public class AccessQueryDto extends PageQueryDto {
    @Schema(description = "账号、昵称或页面/角色名称关键词，最多64个码点")
    private String keyword;

    @Schema(description = "包含当前已分配但未生效的关系")
    private boolean includeInactive;

    @Schema(description = "按页面查用户或来源角色时，可限定一个用户ID；包含未生效时可解释未授予")
    private String userId;

    @Schema(description = "按用户查页面时，可限定模块编码")
    private String moduleKey;
}
