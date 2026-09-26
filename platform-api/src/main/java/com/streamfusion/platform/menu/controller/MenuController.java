package com.streamfusion.platform.menu.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import com.streamfusion.platform.menu.pojo.dto.MenuTreeQueryDto;
import com.streamfusion.platform.menu.pojo.dto.MenuUpdateDto;
import com.streamfusion.platform.menu.pojo.dto.MenuWriteDto;
import com.streamfusion.platform.menu.pojo.vo.MenuNodeVo;
import com.streamfusion.platform.menu.service.MenuService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.ModelAttribute;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;

@Tag(name = "菜单管理")
@RestController
@RequiredArgsConstructor
@RequestMapping("/menus")
@ModuleAccess("menu")
public class MenuController {
    private final MenuService menus;

    @Operation(summary = "查询菜单树")
    @GetMapping
    public R<List<MenuNodeVo>> tree(@ParameterObject @ModelAttribute MenuTreeQueryDto query) {
        return R.success(menus.tree(query));
    }

    @Operation(summary = "创建菜单")
    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    public R<MenuNodeVo> create(
            @RequestBody MenuWriteDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        MenuNodeVo menu = menus.create(input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.LOCATION, request.getContextPath() + "/menus/" + menu.id());
        return withEtag(menu, response);
    }

    @Operation(summary = "修改菜单")
    @PutMapping("/{id}")
    public R<MenuNodeVo> update(
            @PathVariable String id,
            @RequestBody MenuUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return withEtag(menus.update(id, input, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "删除菜单")
    @DeleteMapping("/{id}")
    public R<Void> delete(
            @PathVariable String id,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        menus.delete(id, VersionHeader.require(ifMatch), AuditContextDto.from(request));
        return R.success();
    }

    private static R<MenuNodeVo> withEtag(MenuNodeVo menu, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(menu.version()));
        return R.success(menu);
    }
}
