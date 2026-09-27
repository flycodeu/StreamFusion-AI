package com.streamfusion.platform.auth.captcha;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.awt.Color;
import java.awt.Font;
import java.awt.RenderingHints;
import java.awt.geom.QuadCurve2D;
import java.awt.image.BufferedImage;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.security.SecureRandom;
import java.util.concurrent.Semaphore;
import javax.imageio.ImageIO;
import org.springframework.stereotype.Component;

/** Fixed-size PNG generation with bounded work; the answer is never written into image metadata. */
@Component
public class CaptchaImageGenerator {
    private static final String LETTERS = "ABCDEFGHJKLMNPQRSTUVWXYZ";
    private static final String DIGITS = "23456789";
    private final SecureRandom random = new SecureRandom();
    private final Semaphore rendering = new Semaphore(4);

    public record Generated(String answer, byte[] image) {
        @Override
        public String toString() {
            return "Generated[REDACTED]";
        }
    }

    public Generated generate() {
        if (!rendering.tryAcquire()) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        try {
            char[] code = new char[5];
            for (int i = 0; i < code.length; i++) {
                String alphabet = i < 3 ? LETTERS : DIGITS;
                code[i] = alphabet.charAt(random.nextInt(alphabet.length()));
            }
            for (int i = code.length - 1; i > 0; i--) {
                int other = random.nextInt(i + 1);
                char saved = code[i];
                code[i] = code[other];
                code[other] = saved;
            }
            var image = new BufferedImage(168, 56, BufferedImage.TYPE_INT_RGB);
            var graphics = image.createGraphics();
            try {
                graphics.setRenderingHint(
                        RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON);
                graphics.setColor(new Color(240, 245, 250));
                graphics.fillRect(0, 0, 168, 56);
                for (int i = 0; i < 48; i++) {
                    graphics.setColor(
                            new Color(
                                    130 + random.nextInt(100),
                                    130 + random.nextInt(100),
                                    130 + random.nextInt(100)));
                    graphics.fillOval(random.nextInt(168), random.nextInt(56), 2, 2);
                }
                graphics.setFont(new Font(Font.SANS_SERIF, Font.BOLD, 30));
                for (int i = 0; i < code.length; i++) {
                    var character = (java.awt.Graphics2D) graphics.create();
                    try {
                        character.setColor(
                                new Color(
                                        30 + random.nextInt(60),
                                        40 + random.nextInt(70),
                                        70 + random.nextInt(80)));
                        character.translate(14 + i * 29, 38 + random.nextInt(7) - 3);
                        character.rotate((random.nextDouble() - 0.5) * 0.45);
                        character.drawString(String.valueOf(code[i]), 0, 0);
                    } finally {
                        character.dispose();
                    }
                }
                graphics.setColor(new Color(120, 150, 175, 150));
                for (int i = 0; i < 3; i++) {
                    graphics.draw(
                            new QuadCurve2D.Double(
                                    0,
                                    random.nextInt(56),
                                    84,
                                    random.nextInt(56),
                                    168,
                                    random.nextInt(56)));
                }
            } finally {
                graphics.dispose();
            }
            var bytes = new ByteArrayOutputStream(8192);
            if (!ImageIO.write(image, "png", bytes) || bytes.size() > 32 * 1024)
                throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
            return new Generated(new String(code), bytes.toByteArray());
        } catch (IOException failure) {
            throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        } finally {
            rendering.release();
        }
    }
}
