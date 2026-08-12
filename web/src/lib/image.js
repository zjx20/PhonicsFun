// 图片上行前的 canvas 压缩，导入向导（拍照识词）与 AI 老师（拍照给老师看）
// 共用：省流量、控 token，也把任意格式统一成 JPEG。

function loadImageElement(file) {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => resolve({ img, url });
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error('无法读取该图片，请换一张试试'));
    };
    img.src = url;
  });
}

/** 压缩为长边 ≤maxSide 的 JPEG Blob。 */
export async function compressImage(file, maxSide, quality) {
  const { img, url } = await loadImageElement(file);
  try {
    const scale = Math.min(1, maxSide / Math.max(img.naturalWidth, img.naturalHeight));
    const width = Math.max(1, Math.round(img.naturalWidth * scale));
    const height = Math.max(1, Math.round(img.naturalHeight * scale));
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    canvas.getContext('2d').drawImage(img, 0, 0, width, height);
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
    if (!blob) throw new Error('图片处理失败，请换一张试试');
    return blob;
  } finally {
    URL.revokeObjectURL(url);
  }
}
