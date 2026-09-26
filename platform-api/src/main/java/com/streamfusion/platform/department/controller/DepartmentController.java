package com.streamfusion.platform.department.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import com.streamfusion.platform.department.pojo.dto.DepartmentTreeQueryDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentUpdateDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentWriteDto;
import com.streamfusion.platform.department.pojo.vo.DepartmentNodeVo;
import com.streamfusion.platform.department.service.DepartmentService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@Tag(name = "部门管理")
@RestController
@RequiredArgsConstructor
@RequestMapping("/departments")
@ModuleAccess("department")
public class DepartmentController {
    private final DepartmentService service;

    @Operation(summary = "查询部门树")
    @GetMapping
    public R<List<DepartmentNodeVo>> tree(
            @ParameterObject @ModelAttribute DepartmentTreeQueryDto query) {
        return R.success(service.tree(query));
    }

    @Operation(summary = "创建部门")
    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    public R<DepartmentNodeVo> create(
            @RequestBody DepartmentWriteDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        DepartmentNodeVo dept = service.create(input, AuditContextDto.from(request));
        response.setHeader(
                HttpHeaders.LOCATION, request.getContextPath() + "/departments/" + dept.id());
        return withEtag(dept, response);
    }

    @Operation(summary = "修改部门")
    @PutMapping("/{id}")
    public R<DepartmentNodeVo> update(
            @PathVariable String id,
            @RequestBody DepartmentUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return withEtag(service.update(id, input, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "删除部门")
    @DeleteMapping("/{id}")
    public R<Void> delete(
            @PathVariable String id,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        service.delete(id, VersionHeader.require(ifMatch), AuditContextDto.from(request));
        return R.success();
    }

    private static R<DepartmentNodeVo> withEtag(
            DepartmentNodeVo dept, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(dept.version()));
        return R.success(dept);
    }
}
