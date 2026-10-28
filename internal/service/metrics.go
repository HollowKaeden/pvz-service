package service

// Metrics считает бизнес-события. Сервисы вызывают его только после того,
// как изменение записано в базу (транзакция закоммичена): откаченное
// действие не считается.
//
// Город и тип товара к этому моменту уже проверены по справочникам, поэтому
// набор их значений в метриках ограничен.
type Metrics interface {
	PVZCreated(city string)
	ReceptionOpened()
	ReceptionClosed()
	ProductAdded(productType string)
}
