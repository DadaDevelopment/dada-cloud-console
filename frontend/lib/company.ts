/**
 * Legal identity of the entity that operates DADA Cloud. Business customers
 * paying by bank transfer look for this on the public site before they will
 * sign anything, and 152-ФЗ requires the personal-data operator to be named,
 * so these values are public by design rather than secret.
 *
 * Single source for the marketing site: the requisites page, the footer line,
 * the privacy/terms "Оператор" sections and the Organization JSON-LD all read
 * from here. The backend prints its own copy on generated invoices from the
 * PLATFORM_* environment variables (backend/internal/config/config.go); the two
 * describe the same entity and must be changed together.
 */
export const COMPANY = {
  shortName: 'ООО "ДАДА ДЕВЕЛОПМЕНТ"',
  fullName:
    'ОБЩЕСТВО С ОГРАНИЧЕННОЙ ОТВЕТСТВЕННОСТЬЮ "БЮРО РАЗРАБОТКИ ПРОГРАММНОГО ОБЕСПЕЧЕНИЯ "ДАДА ДЕВЕЛОПМЕНТ"',
  shortNameEn: "DADA Development LLC",
  inn: "7807402712",
  kpp: "780701001",
  ogrn: "1257800096839",
  legalAddress:
    "198335, г. Санкт-Петербург, вн. тер. г. муниципальный округ Южно-Приморский, пр-кт Героев, д. 23, литера А, кв. 596",
  email: "hello@dada-tuda.ru",
  bank: {
    account: "40702810310001995295",
    correspondentAccount: "30101810145250000974",
    bic: "044525974",
    name: 'АО "ТБанк"',
    inn: "7710140679",
    kpp: "771301001",
  },
} as const;
