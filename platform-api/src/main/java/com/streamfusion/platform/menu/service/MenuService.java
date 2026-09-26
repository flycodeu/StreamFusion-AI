package com.streamfusion.platform.menu.service;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.menu.pojo.dto.MenuTreeQueryDto;
import com.streamfusion.platform.menu.pojo.dto.MenuUpdateDto;
import com.streamfusion.platform.menu.pojo.dto.MenuWriteDto;
import com.streamfusion.platform.menu.pojo.vo.MenuNodeVo;
import com.streamfusion.platform.menu.pojo.vo.MenuRouteVo;
import java.util.List;

public interface MenuService {
    List<MenuNodeVo> tree(MenuTreeQueryDto query);

    List<MenuRouteVo> routes(long userId);

    MenuNodeVo create(MenuWriteDto input, AuditContextDto context);

    MenuNodeVo update(String id, MenuUpdateDto input, AuditContextDto context);

    void delete(String id, String version, AuditContextDto context);
}
