// Development test bridge: reuse the browser implementation, pass secrets via stdin.
import { encryptSensitiveRequest } from "../web/src/requestCipher.ts";
let input = "";
for await (const chunk of process.stdin) input += chunk;
const { challenge, path, data } = JSON.parse(input);
process.stdout.write(
  JSON.stringify(await encryptSensitiveRequest(challenge, path, data)),
);
