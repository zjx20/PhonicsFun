<script>
  // 设置页（#/settings）：AI 老师的性格提示词、音色与听说灵敏度（VAD）。
  // 保存到后端 settings.json，下次开启 AI 老师时生效（会话中途不热更新）。
  import * as api from '../lib/api.js';
  import { toastError, toastSuccess } from '../lib/toast.svelte.js';

  const PROMPT_LIMIT = 2000; // 与后端 store.MaxTeacherPromptLen 一致

  // Gemini Live 预置音色（选项会随官方增减，后端不做白名单）
  const VOICES = ['Kore', 'Puck', 'Charon', 'Fenrir', 'Aoede', 'Leda', 'Orus', 'Zephyr'];

  // VAD 默认值与滑杆范围：默认与后端 llm.teacherVAD* 常量一致，
  // 范围与 store.Min/MaxTeacherVAD*Ms 校验一致。保存时等于默认的值发 0
  //（= 跟随服务端默认，后端将来调优默认值时自动跟进）。
  const VAD_PREFIX_DEFAULT = 40;
  const VAD_SILENCE_DEFAULT = 600;
  const VAD_PREFIX_MIN = 20, VAD_PREFIX_MAX = 500;
  const VAD_SILENCE_MIN = 200, VAD_SILENCE_MAX = 2000;

  let teacherPrompt = $state('');
  let teacherVoice = $state('');
  let vadPrefix = $state(VAD_PREFIX_DEFAULT);
  let vadSilence = $state(VAD_SILENCE_DEFAULT);
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');

  $effect(() => {
    load();
  });

  async function load() {
    loading = true;
    error = '';
    try {
      const s = await api.getSettings();
      teacherPrompt = s.teacherPrompt ?? '';
      teacherVoice = s.teacherVoice ?? '';
      vadPrefix = s.teacherVadPrefixMs || VAD_PREFIX_DEFAULT;
      vadSilence = s.teacherVadSilenceMs || VAD_SILENCE_DEFAULT;
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }

  async function save() {
    saving = true;
    try {
      await api.putSettings({
        teacherPrompt,
        teacherVoice,
        teacherVadPrefixMs: vadPrefix === VAD_PREFIX_DEFAULT ? 0 : vadPrefix,
        teacherVadSilenceMs: vadSilence === VAD_SILENCE_DEFAULT ? 0 : vadSilence,
      });
      toastSuccess('已保存，下次开启 AI 老师时生效');
    } catch (err) {
      toastError(err.message);
    } finally {
      saving = false;
    }
  }

  function resetPrompt() {
    teacherPrompt = '';
    teacherVoice = '';
    vadPrefix = VAD_PREFIX_DEFAULT;
    vadSilence = VAD_SILENCE_DEFAULT;
  }
</script>

<header class="page-header">
  <a class="back-btn" href="#/">←</a>
  <h1>设置</h1>
</header>

<main class="page">
  {#if loading}
    <div class="center-hint">
      <span class="spinner"></span>
      <p>加载中…</p>
    </div>
  {:else if error}
    <div class="center-hint">
      <p class="error-text">加载失败：{error}</p>
      <button class="btn btn-primary" onclick={load}>重试</button>
    </div>
  {:else}
    <section class="field">
      <h2 class="field-title">🧑‍🏫 AI 老师性格设定</h2>
      <p class="field-desc">
        自定义老师的名字、性格和说话风格，会拼接在内置教学指令之后。
        例如：“你叫 Lily，是一只爱笑的小兔子老师，喜欢用动物打比方，夸人的花样特别多。”
      </p>
      <textarea
        class="prompt-input"
        bind:value={teacherPrompt}
        maxlength={PROMPT_LIMIT}
        rows="6"
        placeholder="留空则使用默认的温柔耐心老师"
      ></textarea>
      <p class="char-count">{teacherPrompt.length} / {PROMPT_LIMIT}</p>
    </section>

    <section class="field">
      <h2 class="field-title">🔊 老师音色</h2>
      <p class="field-desc">AI 老师说话的声音（Gemini 预置音色）。</p>
      <select class="voice-select" bind:value={teacherVoice}>
        <option value="">默认（跟随服务端 VOICE 配置）</option>
        {#each VOICES as v}
          <option value={v}>{v}</option>
        {/each}
      </select>
    </section>

    <section class="field">
      <h2 class="field-title">🎙️ 听说灵敏度</h2>
      <p class="field-desc">
        控制 AI 老师怎么判定“孩子开始说话了 / 说完了”。孩子跟读单词往往又短又轻，
        默认值已按这个场景调过；只在遇到下面描述的症状时再微调。
      </p>

      <div class="vad-item">
        <div class="vad-label">
          <span>开口判定时长</span>
          <span class="vad-value">{vadPrefix} 毫秒{vadPrefix === VAD_PREFIX_DEFAULT ? '（默认）' : ''}</span>
        </div>
        <input
          type="range"
          min={VAD_PREFIX_MIN}
          max={VAD_PREFIX_MAX}
          step="10"
          bind:value={vadPrefix}
        />
        <div class="vad-ends"><span>← 更灵敏</span><span>更抗噪音 →</span></div>
        <p class="field-desc">
          老师要听到多长的<b>连续人声</b>才认定“孩子开口了”。
          调小：再短促的发音（比如跟读 cat 这种单音节词）也不会被漏掉；
          调大：电视声、关门声等环境噪音不容易被误当成孩子开口、打断老师说话。
          <br />症状对照：<b>孩子跟读了老师却没反应 → 调小</b>；
          <b>没人说话老师却总被莫名打断 → 调大</b>。
        </p>
      </div>

      <div class="vad-item">
        <div class="vad-label">
          <span>接话等待时长</span>
          <span class="vad-value">{vadSilence} 毫秒{vadSilence === VAD_SILENCE_DEFAULT ? '（默认）' : ''}</span>
        </div>
        <input
          type="range"
          min={VAD_SILENCE_MIN}
          max={VAD_SILENCE_MAX}
          step="50"
          bind:value={vadSilence}
        />
        <div class="vad-ends"><span>← 接话更快</span><span>更耐心 →</span></div>
        <p class="field-desc">
          孩子停止说话后，老师要等多久的<b>安静</b>才确认“说完了”、开始回应。
          调小：孩子说完一个词老师马上接话；
          调大：孩子想词、喘气的停顿不会被当成“说完了”，整句话不被拦腰打断。
          <br />症状对照：<b>老师反应太慢 → 调小</b>；
          <b>孩子话说到一半总被老师抢话 → 调大</b>。
        </p>
      </div>
    </section>

    <div class="actions">
      <button class="btn btn-primary btn-big" onclick={save} disabled={saving}>
        {saving ? '保存中…' : '保存'}
      </button>
      <button class="btn btn-ghost" onclick={resetPrompt} disabled={saving}>恢复默认</button>
    </div>
  {/if}
</main>

<style>
  .field {
    margin-bottom: 24px;
  }
  .field-title {
    font-size: 17px;
    margin-bottom: 6px;
  }
  .field-desc {
    font-size: 13px;
    color: var(--muted);
    line-height: 1.6;
    margin-bottom: 10px;
  }
  .prompt-input {
    width: 100%;
    border: 1px solid #ddd;
    border-radius: 12px;
    padding: 12px;
    font-size: 15px;
    line-height: 1.6;
    font-family: inherit;
    resize: vertical;
  }
  .prompt-input:focus {
    outline: 2px solid var(--primary);
    border-color: transparent;
  }
  .char-count {
    text-align: right;
    font-size: 12px;
    color: var(--muted);
    margin-top: 4px;
  }
  .voice-select {
    width: 100%;
    min-height: 44px;
    border: 1px solid #ddd;
    border-radius: 12px;
    padding: 0 12px;
    font-size: 15px;
    background: #fff;
  }
  .vad-item {
    margin-top: 16px;
    padding: 12px;
    border: 1px solid #eee;
    border-radius: 12px;
  }
  .vad-label {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    font-size: 15px;
    font-weight: 600;
    margin-bottom: 6px;
  }
  .vad-value {
    font-weight: 400;
    font-size: 13px;
    color: var(--muted);
  }
  .vad-item input[type='range'] {
    width: 100%;
    min-height: 32px;
    accent-color: var(--primary);
  }
  .vad-ends {
    display: flex;
    justify-content: space-between;
    font-size: 12px;
    color: var(--muted);
    margin-bottom: 8px;
  }
  .vad-item .field-desc {
    margin-bottom: 0;
  }
  .actions {
    display: flex;
    gap: 12px;
    align-items: center;
  }
</style>
