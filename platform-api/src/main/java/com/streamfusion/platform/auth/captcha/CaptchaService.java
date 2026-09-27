package com.streamfusion.platform.auth.captcha;

import com.streamfusion.platform.auth.pojo.vo.CaptchaVo;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import jakarta.servlet.http.HttpServletRequest;
import java.security.SecureRandom;
import java.time.Clock;
import java.util.HexFormat;
import java.util.Locale;
import lombok.RequiredArgsConstructor;
import org.springframework.security.authentication.AnonymousAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class CaptchaService {
    private final CaptchaStore store;
    private final CaptchaImageGenerator images;
    private final Clock clock;
    private final SecureRandom random = new SecureRandom();

    public CaptchaVo create(HttpServletRequest request) {
        requireAnonymous();
        var session = request.getSession(false);
        if (session == null) throw BusinessException.error(ErrorCode.CSRF_INVALID);
        byte[] idBytes = new byte[16];
        random.nextBytes(idBytes);
        String id = HexFormat.of().formatHex(idBytes);
        var generated = images.generate();
        store.replace(session.getId(), id, answerHash(id, generated.answer()), generated.image());
        return new CaptchaVo(
                id, clock.instant().plus(CaptchaStore.LIFETIME), "/auth/captcha/" + id + "/image");
    }

    public byte[] image(HttpServletRequest request, String id) {
        requireAnonymous();
        var session = request.getSession(false);
        if (session == null || !validId(id)) throw invalid();
        byte[] image = store.image(session.getId(), id);
        if (image == null) throw invalid();
        return image;
    }

    public void verify(HttpServletRequest request, String id, String answer) {
        var session = request.getSession(false);
        if (session == null || !validId(id)) throw invalid();
        // A malformed answer still consumes this exact captcha, but never a newer captcha ID.
        String hash =
                answer != null && answer.matches("[A-Za-z0-9]{5}") ? answerHash(id, answer) : "";
        if (!store.consume(session.getId(), id, hash)) throw invalid();
    }

    private static String answerHash(String id, String answer) {
        return CaptchaStore.digest(id + ":" + answer.toUpperCase(Locale.ROOT));
    }

    private static boolean validId(String id) {
        return id != null && id.matches("[0-9a-f]{32}");
    }

    private static void requireAnonymous() {
        var auth = SecurityContextHolder.getContext().getAuthentication();
        if (auth != null
                && auth.isAuthenticated()
                && !(auth instanceof AnonymousAuthenticationToken))
            throw BusinessException.error(ErrorCode.CONFLICT);
    }

    private static BusinessException invalid() {
        return BusinessException.error(ErrorCode.CAPTCHA_INVALID);
    }
}
