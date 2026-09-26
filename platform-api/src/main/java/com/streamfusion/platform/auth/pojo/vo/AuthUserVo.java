package com.streamfusion.platform.auth.pojo.vo;

import com.fasterxml.jackson.annotation.JsonProperty;
import com.streamfusion.platform.access.pojo.vo.RoleVo;
import com.streamfusion.platform.menu.pojo.vo.MenuRouteVo;
import com.streamfusion.platform.user.pojo.vo.UserProfileVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 当前登录用户资料和授权结果。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "当前登录用户资料和授权结果")
public class AuthUserVo {
    /** 当前用户可查看及维护的个人资料。 */
    @Schema(description = "当前用户个人资料")
    private UserProfileVo user;

    /** 当前用户可访问的功能模块；待改密会话返回空列表。 */
    @Schema(description = "功能模块列表")
    private List<String> modules;

    /** 当前有效角色的最小信息，由前端身份快照使用。 */
    @Schema(description = "角色列表")
    private List<RoleVo> roles;

    /** Current user's enabled page routes and only their directory ancestors. */
    @Schema(description = "页面路由树")
    private List<MenuRouteVo> routes;

    /** 当前会话是否具有可用的超级管理员资格。 */
    @JsonProperty("isSuperAdmin")
    @Schema(description = "超级管理员状态")
    private boolean superAdmin;
}
