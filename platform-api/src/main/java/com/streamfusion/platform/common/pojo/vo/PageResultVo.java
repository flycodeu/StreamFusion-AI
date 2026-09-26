package com.streamfusion.platform.common.pojo.vo;

import com.baomidou.mybatisplus.core.metadata.IPage;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 公共分页出参，放在 R.data 中，不暴露 ORM 的内部分页及排序配置。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "分页结果")
public class PageResultVo<T> {
    /** 当前页的业务 VO 集合，不返回持久化实体。 */
    @Schema(description = "当前页记录")
    private List<T> items;

    /** 本次请求的页码；超出末页时仍保留请求值。 */
    @Schema(description = "当前页码", minimum = "1")
    private int page;

    /** 本次请求的每页记录数。 */
    @Schema(description = "每页记录数", minimum = "1", maximum = "100")
    private int size;

    /** 满足全部筛选条件的总记录数。 */
    @Schema(description = "符合条件的总记录数", minimum = "0")
    private long total;

    public static <T> PageResultVo<T> from(IPage<?> page, List<T> items) {
        return new PageResultVo<>(
                List.copyOf(items),
                Math.toIntExact(page.getCurrent()),
                Math.toIntExact(page.getSize()),
                page.getTotal());
    }
}
