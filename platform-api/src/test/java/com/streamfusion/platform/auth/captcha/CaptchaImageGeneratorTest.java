package com.streamfusion.platform.auth.captcha;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import javax.imageio.ImageIO;
import org.junit.jupiter.api.Test;

class CaptchaImageGeneratorTest {
    @Test
    void generatesBoundedPngWithBothCharacterClassesWithoutAnswerMetadata() throws Exception {
        var generator = new CaptchaImageGenerator();
        var answers = new HashSet<String>();
        for (int i = 0; i < 20; i++) {
            var generated = generator.generate();
            assertThat(generated.answer())
                    .matches("[A-HJ-NP-Z2-9]{5}")
                    .containsPattern("[A-Z]")
                    .containsPattern("[2-9]");
            assertThat(generated.toString()).doesNotContain(generated.answer());
            assertThat(generated.image().length).isLessThanOrEqualTo(32768);
            var pixels = ImageIO.read(new ByteArrayInputStream(generated.image()));
            assertThat(pixels.getWidth()).isEqualTo(168);
            assertThat(pixels.getHeight()).isEqualTo(56);
            var chunks = java.nio.ByteBuffer.wrap(generated.image());
            chunks.position(8); // PNG signature.
            while (chunks.remaining() > 0) {
                int length = chunks.getInt();
                byte[] type = new byte[4];
                chunks.get(type);
                assertThat(new String(type, StandardCharsets.US_ASCII))
                        .isNotIn("tEXt", "iTXt", "zTXt");
                chunks.position(chunks.position() + length + 4); // Payload and CRC.
            }
            answers.add(generated.answer());
        }
        assertThat(answers.size()).isGreaterThan(1);
    }
}
