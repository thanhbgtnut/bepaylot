import json
import sys

import requests

HOST = "http://127.0.0.1:1234"
MODEL = "cloudflare_clef-flash"
URL = f"{HOST}/v1/systemone"

DOCUMENT_TEXT = """**ÚY NHIỆM CHI (PAYMENT ORDER)**
Ngày (Date): 20
Số GD (Transaction no.):
MÃ GDV (Teller code):
Loại tiền (Currency): VND
Tên đơn vị chuyển tiền (Payer's name): Công ty TNHH MTV ICT 897
Số tiền (Amount in figures): 56.575.757
Tên địa chỉ (A.MB's branch): Pleika - Gia Lai
Tỉnh/Thành phố (Province/City): Gia Lai
Ngân hàng thụ hưởng (Beneficiary's Bank): NH TMCP Ngoại Thương Việt Nam - CN Tp.HCM
Ngân hàng chuyển tiền (Beneficiary's Bank): Ngân hàng United Overseas - chi nhánh TPHCM
Số tiền bằng số (Amount in figures): 60.000.000
Số tiền bằng chữ (Amount in words): TKNH TK vay
Nội dung (Detail): Chuyển trả tiền
ĐƠN VỊ CHUYỂN TIỀN (Payer): NGÂN HÀNG A - MB (Bank A-MB)
Ngày ghi sổ (settlement date):
NGÂN HÀNG B (Bank B): NGÂN HÀNG A - MB (Bank A-MB)
Ngày ghi sổ (settlement date):
Kê toán trưởng (Account holder):
Chủ tài khoản (Account holder):
Giao dịch viên (Teller):
Kiểm soát (Supervisor):
Giao dịch viên (Teller):
Kiểm soát (Supervisor):"""

# Các loại chứng từ để model chọn (key = nhãn, value = mô tả hoặc None)
DOCUMENT_TYPES = {
    "uy_nhiem_chi": "Ủy nhiệm chi / Payment order",
    "giay_nop_tien": "Giấy nộp tiền / Cash deposit slip",
    "giay_rut_tien": "Giấy rút tiền / Cash withdrawal slip",
    "hoa_don_gtgt": "Hóa đơn giá trị gia tăng / VAT invoice",
    "hop_dong": "Hợp đồng / Contract",
    "giay_chung_nhan": "Giấy chứng nhận / Certificate",
    "khac": "Loại chứng từ khác",
}

payload = {
    "model": MODEL,
    "state": f"Nội dung chứng từ:\n{DOCUMENT_TEXT}",
    "questions": {
        "document_type": {
            "type": "choice",
            "instructions": "Đây là loại chứng từ nào?",
            "criteria": DOCUMENT_TYPES,
        },
    },
}


def main():
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
