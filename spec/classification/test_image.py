import base64
import json
import mimetypes
import sys

import requests

from test import DOCUMENT_TYPES, MODEL, URL


def to_data_url(image_path):
    # Server chỉ nhận ảnh dạng data URL base64 inline (không nhận HTTP URL hay đường dẫn file)
    mime = mimetypes.guess_type(image_path)[0] or "image/jpeg"
    with open(image_path, "rb") as f:
        b64 = base64.b64encode(f.read()).decode()
    return f"data:{mime};base64,{b64}"


def main():
    if len(sys.argv) != 2:
        sys.exit(f"Cách dùng: python3 {sys.argv[0]} <file ảnh>")

    payload = {
        "model": MODEL,
        "state": "Ảnh chứng từ:",
        "images": [to_data_url(sys.argv[1])],
        "questions": {
            "document_type": {
                "type": "choice",
                "instructions": "Đây là loại chứng từ nào?",
                "criteria": DOCUMENT_TYPES,
            },
        },
    }

    try:
        resp = requests.post(URL, json=payload, timeout=120)
    except requests.RequestException as e:
        sys.exit(f"Không gọi được {URL}: {e}")

    print(f"HTTP {resp.status_code} ({resp.elapsed.total_seconds():.2f}s)")
    try:
        print(json.dumps(resp.json(), ensure_ascii=False, indent=2))
    except ValueError:
        print(resp.text)


if __name__ == "__main__":
    main()
