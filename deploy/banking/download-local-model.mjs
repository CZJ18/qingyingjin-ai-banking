import { mkdir, stat, readFile, writeFile, appendFile } from 'node:fs/promises'
import { createReadStream, createWriteStream } from 'node:fs'
import { resolve } from 'node:path'
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { pipeline } from 'node:stream/promises'

const upgrade = process.argv.includes('--3b')
const dir = resolve(import.meta.dirname, '../../output/local-model', upgrade ? '3b' : '.')
const total = upgrade ? 2104932768 : 1117320736, chunk = 4 * 1024 * 1024
const expectedHash = upgrade ? '626b4a6678b86442240e33df819e00132d3ba7dddfe1cdc4fbb18e0a9615c62d' : '6a1a2eb6d15622bf3c96857206351ba97e1af16c30d7a74ee38970e434e9407e'
const size = upgrade ? '3' : '1.5'
const url = `https://huggingface.co/Qwen/Qwen2.5-${size}B-Instruct-GGUF/resolve/main/qwen2.5-${size}b-instruct-q4_k_m.gguf?download=true`
await mkdir(resolve(dir, 'parts4'), { recursive: true })
let next = 0, done = 0
const count = Math.ceil(total / chunk)
async function download(i) {
  const start = i * chunk, end = Math.min(total - 1, start + chunk - 1)
  const file = resolve(dir, `parts4/${i}.bin`), headers = resolve(dir, `parts4/${i}.headers`)
  let have = 0
  try {
    if ((await readFile(headers, 'utf8')).includes(`bytes ${start}-${end}/${total}`)) have = (await stat(file)).size
  } catch {}
  if (have > end - start + 1) throw new Error(`Invalid cached chunk ${i}`)
  if (!have) await writeFile(file, '')
  for (let attempt = 0; have < end - start + 1 && attempt < 10; attempt++) {
    const offset = start + have, tail = file + '.tail', head = headers + '.tail'
    const args = ['-f', '-L', '--connect-timeout', '15', '--max-time', '45', '-s', '--range', `${offset}-${end}`, '-D', head, '-o', tail]
    if (process.env.MODEL_DOWNLOAD_PROXY) args.push('--proxy', process.env.MODEL_DOWNLOAD_PROXY)
    args.push(url + `&chunk4=${i}&offset=${offset}`)
    await new Promise((ok, fail) => {
      const p = spawn('curl.exe', args, { stdio: 'ignore', windowsHide: true })
      p.on('error', fail); p.on('exit', ok)
    })
    try {
      const data = await readFile(tail), response = await readFile(head, 'utf8')
      if (!response.includes(`bytes ${offset}-${end}/${total}`) || !data.length || data.length > end - offset + 1) continue
      await appendFile(file, data)
      have += data.length
      await writeFile(headers, `content-range: bytes ${start}-${end}/${total}`)
    } catch {}
  }
  if (have !== end - start + 1) throw new Error(`Model chunk ${i} incomplete; rerun to resume verified bytes`)
}
await Promise.all(Array.from({ length: Number(process.env.MODEL_DOWNLOAD_WORKERS || 8) }, async () => {
  while (next < count) {
    const i = next++
    await download(i)
    done++
    if (done % 4 === 0 || done === count) console.log(`Verified model chunks: ${done}/${count}`)
  }
}))
const target = resolve(dir, 'qwen-complete.gguf')
async function* chunks() { for (let i = 0; i < count; i++) for await (const data of createReadStream(resolve(dir, `parts4/${i}.bin`))) yield data }
await pipeline(chunks(), createWriteStream(target))
const hash = createHash('sha256')
for await (const data of createReadStream(target)) hash.update(data)
if (hash.digest('hex') !== expectedHash) throw new Error('Official model SHA256 mismatch; do not start inference')
console.log('Official Qwen model assembled and SHA256 verified')
