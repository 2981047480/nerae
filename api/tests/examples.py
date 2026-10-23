"""Independent synthetic examples, never generated from the schemas under test.

Numbers/foods are validator fixtures, not nutrition recommendations or claims of
clinical safety. IDs are fictional decimal strings matching the published ID
syntax. Every caller gets a new object, so negative mutations cannot leak.
"""


def training_content():
    return {
        "kind": "training",
        "weeks": 2,
        "days": [{
            "weekIndex": 1,
            "dayIndex": 2,
            "title": "测试训练日",
            "type": "strength",
            "items": [{"exerciseId": "501", "sets": 3, "repsMin": 8, "repsMax": 10}],
        }],
    }


def diet_content():
    return {
        "kind": "diet",
        "nutritionBand": {"energyMinKcal": 1800, "energyMaxKcal": 2000,
                          "proteinMinGPerKg": 1.0, "proteinMaxGPerKg": 1.5,
                          "basis": "仅为静态格式测试，不是营养处方"},
        "dietType": "normal",
        "meals": [{"dayIndex": 1, "slotKey": "breakfast", "label": "测试早餐",
                   "foods": [{"name": "测试食物", "quantity": {"value": 100, "unit": "g"},
                              "preparation": "cooked",
                              "allergenAssessment": {"status": "unknown", "allergens": []}}]}],
        "allergenAssessment": {"status": "unknown", "allergens": []},
        "notSuitable": "未核对适用性，本测试数据不用于实际执行",
    }


def training_read_content():
    """Independent read fixture includes the server-frozen exercise description."""
    content = training_content()
    content['days'][0]['id'] = '202'
    content['days'][0]['items'][0]['exerciseSnapshot'] = {
        'exerciseId': '501', 'version': 2, 'name': '测试动作',
        'cues': ['测试说明，非训练建议'], 'precautions': [], 'alternatives': [],
    }
    return content


def service_terms():
    return {"title": "四周测试服务", "priceCents": 49900,
            "includes": ["每周计划与异步反馈"], "responseSlaHours": 24}
