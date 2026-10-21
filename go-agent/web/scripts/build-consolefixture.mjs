import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const productRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const outputDir = path.join(productRoot, '.tmp')
mkdirSync(outputDir, { recursive: true })
const binary = path.join(outputDir, `consolefixture${process.platform === 'win32' ? '.exe' : ''}`)
const result = spawnSync('go', ['build', '-o', binary, './tests/consolefixture'], {
  cwd: productRoot, stdio: 'inherit', shell: false,
})
if (result.error) throw result.error
if (result.signal) throw new Error(`Fixture build interrupted: ${result.signal}`)
process.exitCode = result.status ?? 1
