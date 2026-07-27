// 后端 API 的 thin fetch 封装（后端 API 契约的唯一入口）。
// 所有请求走相对路径 /api/...：dev 由 Vite 代理到 Go 后端（见 vite.config.js），
// 生产构建被 Go go:embed 后与 API 同源，因此无需任何绝对地址。
//
// 契约一览：
//   POST   /api/extract                                {text} 或 multipart image → {words:[...]}
//   POST   /api/groups                                 {name?, words:[]} → {id}
//   GET    /api/groups                                 [{id,name,createdAt,total,ready}]
//   GET    /api/groups/{id}                            {..., words:[{word,slug,text,audio,error?}]}
//   DELETE /api/groups/{id}                            ?purge=1 连带删除无引用的词目录
//   GET    /api/words/{slug}                           card.json v2：{schema:2, word, ipa,
//                                                        senses:[{pos,zh,en}], examples:[{en,zh}],
//                                                        syllables:[{text,respell,chunks:[{grapheme,
//                                                        phoneme,respell,anchor_word,silent}]}],
//                                                        generated_at, model}
//   GET    /api/words/{slug}/audio/{word|blend}.wav    音频（支持 Range）
//   GET    /api/words/{slug}/audio/blend.cues.json     blend.wav 的时间标注 {version, sample_rate,
//                                                        cues:[{kind:"chunk"|"syllable"|"tail",
//                                                        syllable, chunk, start_ms, end_ms}]}；
//                                                        404 = 旧数据无 cues，前端须优雅降级
//   POST   /api/words/{slug}/regenerate                {target:"text"|"audio"|"both"} → 202

const JSON_HEADERS = { 'Content-Type': 'application/json' };

async function request(path, options = {}) {
  let res;
  try {
    res = await fetch(path, options);
  } catch (err) {
    if (err.name === 'AbortError') throw err;
    throw new Error('网络请求失败，请确认服务已启动');
  }
  if (!res.ok) {
    let message = `请求失败（HTTP ${res.status}）`;
    try {
      const body = await res.text();
      if (body) {
        try {
          const data = JSON.parse(body);
          message = data.error || data.message || message;
        } catch {
          message = body.slice(0, 120);
        }
      }
    } catch {
      // 读不到响应体时保留默认 message
    }
    throw new Error(message);
  }
  if (res.status === 204) return null;
  const type = res.headers.get('content-type') || '';
  return type.includes('json') ? res.json() : res.text();
}

/** POST /api/extract：从纯文本提取单词 → {words: [...]} */
export function extractFromText(text, signal) {
  return request('/api/extract', {
    method: 'POST',
    headers: JSON_HEADERS,
    body: JSON.stringify({ text }),
    signal,
  });
}

/** POST /api/extract：从图片（multipart 字段 image）提取单词 → {words: [...]} */
export function extractFromImage(blob, signal) {
  const form = new FormData();
  form.append('image', blob, 'photo.jpg');
  return request('/api/extract', { method: 'POST', body: form, signal });
}

/** POST /api/groups → {id} */
export function createGroup({ name, words }) {
  return request('/api/groups', {
    method: 'POST',
    headers: JSON_HEADERS,
    body: JSON.stringify({ name, words }),
  });
}

/** GET /api/groups → [{id,name,createdAt,total,ready}] */
export function listGroups() {
  return request('/api/groups');
}

/** GET /api/groups/{id} → 组详情（含每个单词的 text/audio 状态） */
export function getGroup(id) {
  return request(`/api/groups/${encodeURIComponent(id)}`);
}

/** DELETE /api/groups/{id} → 204 */
export function deleteGroup(id) {
  return request(`/api/groups/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** GET /api/words/{slug} → card.json（v2，结构见文件顶部契约一览） */
export function getCard(slug) {
  return request(`/api/words/${encodeURIComponent(slug)}`);
}

/**
 * blend 音频时间标注地址。version 传 card.generated_at，与音频同一套防缓存机制。
 */
export function blendCuesUrl(slug, version) {
  return `/api/words/${encodeURIComponent(slug)}/audio/blend.cues.json?v=${encodeURIComponent(version || '')}`;
}

/**
 * GET /api/words/{slug}/audio/blend.cues.json → cues 对象。
 * 404（旧数据没有 cues）返回 null 而不是抛错，调用方据此降级（无高亮、无点读）。
 */
export async function getBlendCues(slug, version) {
  let res;
  try {
    res = await fetch(blendCuesUrl(slug, version));
  } catch {
    throw new Error('网络请求失败，请确认服务已启动');
  }
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`请求失败（HTTP ${res.status}）`);
  return res.json();
}

/** POST /api/words/{slug}/regenerate，target: "text" | "audio" | "both" → 202 */
export function regenerateWord(slug, target) {
  return request(`/api/words/${encodeURIComponent(slug)}/regenerate`, {
    method: 'POST',
    headers: JSON_HEADERS,
    body: JSON.stringify({ target }),
  });
}

/**
 * 音频地址，kind: "word" | "blend"。
 * version 传 card.generated_at：重新生成后 generated_at 变化，URL 随之变化，绕开浏览器缓存。
 */
export function wordAudioUrl(slug, kind, version) {
  return `/api/words/${encodeURIComponent(slug)}/audio/${kind}.wav?v=${encodeURIComponent(version || '')}`;
}
