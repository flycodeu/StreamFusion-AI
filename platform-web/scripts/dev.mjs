import { execFile, spawn } from 'node:child_process'
import { realpath } from 'node:fs/promises'
import { fileURLToPath, URL } from 'node:url'
import { parseArgs, promisify } from 'node:util'
import { resolveConfig } from 'vite'

const projectDir = fileURLToPath(new URL('../', import.meta.url))
const viteEntry = fileURLToPath(new URL('../node_modules/vite/bin/vite.js', import.meta.url))
process.chdir(projectDir)

try {
  const { values } = parseArgs({
    options: {
      port: { type: 'string' },
      host: { type: 'string' },
      mode: { type: 'string', short: 'm' },
    },
  })
  const config = await resolveConfig({ mode: values.mode }, 'serve')
  const port = values.port === undefined ? config.server.port : Number(values.port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error('The development port must be an integer between 1 and 65535.')
  }

  if (process.platform === 'win32') {
    const { stdout } = await promisify(execFile)(
      'powershell.exe',
      [
        '-NoProfile',
        '-NonInteractive',
        '-File',
        fileURLToPath(new URL('../../scripts/release-web-port.ps1', import.meta.url)),
        '-Port',
        String(port),
        '-ViteEntry',
        viteEntry,
        '-ViteRealEntry',
        await realpath(viteEntry),
      ],
      { timeout: 15000, windowsHide: true },
    )
    process.stdout.write(stdout)
  }

  const child = spawn(process.execPath, [viteEntry, ...process.argv.slice(2)], {
    cwd: projectDir,
    stdio: 'inherit',
  })
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => child.kill(signal))
  }
  child.on('error', (error) => {
    process.stderr.write(`Cannot start Vite: ${error.message}\n`)
    process.exitCode = 1
  })
  child.on('exit', (code) => {
    process.exitCode = code ?? 0
  })
} catch (error) {
  process.stderr.write(`${error.stderr || error.message}\n`)
  process.exitCode = 1
}
