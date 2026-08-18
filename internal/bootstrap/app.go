    Bank:
      type: object
      required: [name, code, bin, short_name, supported]
      properties:
        id: {type: integer, nullable: true}
        name: {type: string}
        code: {type: string}
        bin: {type: string}
        short_name: {type: string}
        logo: {type: string, format: uri, nullable: true}
        supported: {type: boolean}
    BankListResponse:
      type: object
      required: [banks]
      properties:
        banks: {type: array, items: {$ref: "#/components/schemas/Bank"}}

    Bill:
      type: object
      required: [id, group_id, creditor_member_id, status, subtotal, service_charge, vat, discount, total, version]
      properties:
        id: {type: string, format: uuid}
        group_id: {type: string, format: uuid}
        creditor_member_id: {type: string, format: uuid}
        status: {type: string, enum: [draft, reviewed, finalized, voided]}
        merchant_name: {type: string, nullable: true}
        bill_date: {type: string, format: date-time, nullable: true}
        subtotal: {type: integer, format: int64}
        service_charge: {type: integer, format: int64}
        vat: {type: integer, format: int64}
        discount: {type: integer, format: int64}
        total: {type: integer, format: int64}
        split_method: {type: string, enum: [even, item_ratio, exact, shares, percentage]}
        mismatch_codes: {type: array, items: {type: string}}
        replaces_bill_id: {type: string, format: uuid, nullable: true}
        version: {type: integer}
        finalized_at: {type: string, format: date-time, nullable: true}
        voided_at: {type: string, format: date-time, nullable: true}
        reviewed_at: {type: string, format: date-time, nullable: true}
        reviewed_by_member_id: {type: string, format: uuid, nullable: true}
        images: {type: array, items: {$ref: "#/components/schemas/BillImage"}}
        items: {type: array, items: {$ref: "#/components/schemas/BillItem"}}
        shares: {type: array, items: {$ref: "#/components/schemas/BillShare"}}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}

    BillImage:
      type: object
      required: [id, bill_id, image_key, position]
      properties:
        id: {type: string, format: uuid}
        bill_id: {type: string, format: uuid}
        group_id: {type: string, format: uuid}
        image_key: {type: string}
        position: {type: integer}
        created_at: {type: string, format: date-time}

    BillItem:
      type: object
      required: [id, bill_id, name, quantity, unit_price, line_total, position]
      properties:
        id: {type: string, format: uuid}
        bill_id: {type: string, format: uuid}
        group_id: {type: string, format: uuid}
        name: {type: string}
        quantity: {type: string}
        unit_price: {type: integer, format: int64}
        line_total: {type: integer, format: int64}
        position: {type: integer}
        assignments: {type: array, items: {$ref: "#/components/schemas/BillItemAssignment"}}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}

    BillItemAssignment:
      type: object
      required: [id, bill_item_id, member_id, weight]
      properties:
        id: {type: string, format: uuid}
        bill_item_id: {type: string, format: uuid}
        group_id: {type: string, format: uuid}
        member_id: {type: string, format: uuid}
        weight: {type: string}
        created_at: {type: string, format: date-time}

    BillShare:
      type: object
      required: [id, bill_id, member_id, final_amount]
      properties:
        id: {type: string, format: uuid}
        bill_id: {type: string, format: uuid}
        group_id: {type: string, format: uuid}
        member_id: {type: string, format: uuid}
        item_subtotal: {type: integer, format: int64}
        service_charge_share: {type: integer, format: int64}
        vat_share: {type: integer, format: int64}
        discount_share: {type: integer, format: int64}
        rounding_adjustment: {type: integer, format: int64}
        final_amount: {type: integer, format: int64}
        created_at: {type: string, format: date-time}

    MemberAllocation:
      type: object
      required: [member_id, item_subtotal, service_charge_share, vat_share, discount_share, final_amount]
      properties:
        member_id: {type: string, format: uuid}
        item_subtotal: {type: integer, format: int64}
        service_charge_share: {type: integer, format: int64}
        vat_share: {type: integer, format: int64}
        discount_share: {type: integer, format: int64}
        final_amount: {type: integer, format: int64}

    OCRJob:
      type: object
      required: [id, bill_id, status, provider, attempts, version]
      properties:
        id: {type: string, format: uuid}
        bill_id: {type: string, format: uuid}
        status: {type: string, enum: [queued, processing, succeeded, failed]}
        provider: {type: string}
        attempts: {type: integer}
        candidate: {type: object}
        error_message: {type: string, nullable: true}
        version: {type: integer}
        created_at: {type: string, format: date-time}
        updated_at: {type: string, format: date-time}
        completed_at: {type: string, format: date-time, nullable: true}
