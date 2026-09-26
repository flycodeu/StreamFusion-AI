package com.streamfusion.platform.access.pojo.dto;

import com.streamfusion.platform.access.pojo.vo.RoleVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Value;

/** 一次授权查询得到的角色和页面模块，不跨请求缓存。 */
@Value
@Schema(description = "当前用户权限查询结果")
public class AccessSnapshotDto {
    /** 用户当前有效角色。 */
    @Schema(description = "用户当前有效角色")
    List<RoleVo> roles;

    /** 当前用户可访问的功能模块编码，前端菜单以菜单模块为准。 */
    @Schema(description = "功能模块编码列表")
    List<String> modules;

    /** 是否拥有有效的超级管理员角色。 */
    @Schema(description = "超级管理员状态")
    boolean superAdmin;

    public AccessSnapshotDto(List<RoleVo> roles, List<String> modules, boolean superAdmin) {
        this.roles = copyRoles(roles);
        this.modules = List.copyOf(modules);
        this.superAdmin = superAdmin;
    }

    public List<RoleVo> getRoles() {
        return copyRoles(roles);
    }

    public List<String> getModules() {
        return List.copyOf(modules);
    }

    private static List<RoleVo> copyRoles(List<RoleVo> source) {
        return source.stream()
                .map(role -> new RoleVo(role.getId(), role.getCode(), role.getName()))
                .toList();
    }
}
