package com.streamfusion.platform.auth.guard;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import jakarta.servlet.http.HttpServletRequest;
import java.util.function.Supplier;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class LoginProtection {
    static final String GENERATION = LoginProtection.class.getName() + ".generation";
    private final IpGuardProperties properties;
    private final ClientIpResolver addresses;
    private final LoginGuardStore store;
    private final IpBlockService blocks;

    public <T> T attempt(HttpServletRequest request, Supplier<T> login) {
        try {
            return login.get();
        } catch (BusinessException failure) {
            if (properties.enabled()
                    && (failure.code() == ErrorCode.LOGIN_FAILED
                            || failure.code() == ErrorCode.VALIDATION_ERROR)) {
                String ip = addresses.resolve(request);
                Object saved = request.getAttribute(GENERATION);
                long generation = saved instanceof Long value ? value : blocks.requireAllowed(ip);
                var counter = store.failure(ip, generation);
                if (counter.count() >= properties.failureThreshold()) {
                    blocks.block(ip, generation, counter.count(), AuditContextDto.from(request));
                    blocks.requireAllowed(ip);
                }
            }
            throw failure;
        }
    }
}
