import { useEffect, useState } from 'react'
import {
  Table,
  Card,
  Button,
  Input,
  Modal,
  Form,
  Space,
  Popconfirm,
  message,
  Typography,
  Select,
  DatePicker,
  InputNumber,
  Tabs,
  Radio,
  Tag,
  Switch,
} from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, RedoOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import {
  paymentApi,
  studentApi,
  courseApi,
  hourAccountApi,
  type HourAccount,
} from '@/services/api'

const { Title, Text } = Typography
const { Option } = Select
const { TextArea } = Input

const paymentMethods = [
  { value: 'cash', label: '现金' },
  { value: 'wechat', label: '微信' },
  { value: 'alipay', label: '支付宝' },
  { value: 'bank', label: '银行转账' },
]

const paymentTypes = [
  { value: 'tuition', label: '学费' },
  { value: 'deposit', label: '定金' },
  { value: 'other', label: '其他' },
]

function Finance() {
  const [loading, setLoading] = useState(false)
  const [payments, setPayments] = useState<any[]>([])
  const [accounts, setAccounts] = useState<HourAccount[]>([])
  const [students, setStudents] = useState<any[]>([])
  const [courses, setCourses] = useState<any[]>([])
  const [modalVisible, setModalVisible] = useState(false)
  const [modalType, setModalType] = useState<'create' | 'edit'>('create')
  const [selectedPayment, setSelectedPayment] = useState<any>(null)
  const [form] = Form.useForm()

  const [accountLoading, setAccountLoading] = useState(false)
  const [onlyNeedRenew, setOnlyNeedRenew] = useState(false)
  const [renewVisible, setRenewVisible] = useState(false)
  const [renewSubmitting, setRenewSubmitting] = useState(false)
  const [renewingAccount, setRenewingAccount] = useState<HourAccount | null>(null)
  const [renewForm] = Form.useForm()
  const renewHours = Form.useWatch('hours', renewForm)
  const renewCourseId = Form.useWatch('course_id', renewForm)

  const fetchPayments = async () => {
    try {
      setLoading(true)
      const res: any = await paymentApi.list()
      setPayments(res.list || [])
    } catch (error) {
      console.error('Fetch payments error:', error)
    } finally {
      setLoading(false)
    }
  }

  const fetchAccounts = async () => {
    try {
      setAccountLoading(true)
      const res: any = await hourAccountApi.list({
        need_renew: onlyNeedRenew ? 'true' : undefined,
      })
      setAccounts(res.list || [])
    } catch (error) {
      console.error('Fetch hour accounts error:', error)
    } finally {
      setAccountLoading(false)
    }
  }

  const fetchOptions = async () => {
    try {
      const [studentsRes, coursesRes] = await Promise.all([
        studentApi.list({ page_size: 1000 }),
        courseApi.list(),
      ])
      setStudents((studentsRes as any)?.list || [])
      setCourses((coursesRes as any)?.list || [])
    } catch (error) {
      console.error('Fetch options error:', error)
    }
  }

  useEffect(() => {
    fetchPayments()
    fetchOptions()
  }, [])

  useEffect(() => {
    fetchAccounts()
  }, [onlyNeedRenew])

  const handleCreate = () => {
    setModalType('create')
    setSelectedPayment(null)
    form.resetFields()
    form.setFieldsValue({
      payment_date: dayjs(),
      payment_method: 'wechat',
      type: 'tuition',
    })
    setModalVisible(true)
  }

  const handleEdit = (payment: any) => {
    setModalType('edit')
    setSelectedPayment(payment)
    form.setFieldsValue({
      ...payment,
      payment_date: payment.payment_date ? dayjs(payment.payment_date) : undefined,
    })
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      await paymentApi.delete(id)
      message.success('删除成功')
      fetchPayments()
    } catch (error) {
      console.error('Delete payment error:', error)
    }
  }

  const handleModalSubmit = async () => {
    try {
      const values = await form.validateFields()
      const data = {
        ...values,
        payment_date: values.payment_date.format('YYYY-MM-DD'),
      }

      if (modalType === 'create') {
        await paymentApi.create(data)
        message.success('创建成功')
      } else if (selectedPayment?.id) {
        await paymentApi.update(selectedPayment.id, data)
        message.success('更新成功')
      }
      setModalVisible(false)
      fetchPayments()
    } catch (error) {
      console.error('Modal submit error:', error)
    }
  }

  const handleOpenRenew = (account: HourAccount) => {
    setRenewingAccount(account)
    renewForm.resetFields()
    renewForm.setFieldsValue({
      student_id: account.student_id,
      course_id: account.course_id,
      hours: 1,
      payment_method: 'wechat',
      payment_date: dayjs(),
    })
    setRenewVisible(true)
  }

  const handleRenewSubmit = async () => {
    try {
      const values = await renewForm.validateFields()
      setRenewSubmitting(true)
      await hourAccountApi.renew({
        student_id: values.student_id,
        course_id: values.course_id,
        hours: values.hours,
        payment_method: values.payment_method,
        payment_date: values.payment_date.format('YYYY-MM-DD'),
        remarks: values.remarks,
      })
      message.success('续费成功，学费记录与课时账户已同步更新')
      setRenewVisible(false)
      fetchAccounts()
      fetchPayments()
    } catch (error) {
      console.error('Renew error:', error)
    } finally {
      setRenewSubmitting(false)
    }
  }

  const renewCourse = courses.find((c) => c.id === renewCourseId)
  const renewPrice = renewCourse?.price_per_hour ?? renewingAccount?.price_per_hour ?? 0
  const renewAmount = (renewHours || 0) * renewPrice

  const columns = [
    {
      title: '学员',
      dataIndex: ['student', 'name'],
      key: 'student',
      render: (name: string) => name || '-',
    },
    {
      title: '课程',
      dataIndex: ['course', 'name'],
      key: 'course',
      render: (name: string) => name || '-',
    },
    {
      title: '金额(元)',
      dataIndex: 'amount',
      key: 'amount',
    },
    {
      title: '支付方式',
      dataIndex: 'payment_method',
      key: 'payment_method',
      render: (method: string) => {
        const opt = paymentMethods.find((o) => o.value === method)
        return opt?.label || method
      },
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (type: string) => {
        const opt = paymentTypes.find((o) => o.value === type)
        return opt?.label || type
      },
    },
    {
      title: '日期',
      dataIndex: 'payment_date',
      key: 'payment_date',
    },
    {
      title: '收据号',
      dataIndex: 'receipt_no',
      key: 'receipt_no',
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: any) => (
        <Space size="small">
          <Button type="link" size="small" onClick={() => handleEdit(record)}>
            <EditOutlined /> 编辑
          </Button>
          <Popconfirm
            title="确定删除?"
            onConfirm={() => handleDelete(record.id!)}
            okText="确定"
            cancelText="取消"
          >
            <Button type="link" size="small" danger>
              <DeleteOutlined /> 删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  const accountColumns = [
    {
      title: '学员',
      key: 'student',
      render: (_: any, record: HourAccount) => (
        <span>
          {record.student_name}
          {record.student_phone ? (
            <span style={{ color: '#999', marginLeft: 8 }}>{record.student_phone}</span>
          ) : null}
        </span>
      ),
    },
    {
      title: '课程',
      dataIndex: 'course_name',
      key: 'course_name',
      render: (name: string) => name || '-',
    },
    {
      title: '单价(元/小时)',
      dataIndex: 'price_per_hour',
      key: 'price_per_hour',
      render: (price: number) => price ?? '-',
    },
    {
      title: '总课时',
      dataIndex: 'total_hours',
      key: 'total_hours',
    },
    {
      title: '已用(小时)',
      dataIndex: 'used_hours',
      key: 'used_hours',
    },
    {
      title: '剩余(小时)',
      dataIndex: 'remaining_hours',
      key: 'remaining_hours',
      render: (hours: number) => (
        <Text strong type={hours <= 5 ? 'danger' : undefined}>
          {hours}
        </Text>
      ),
    },
    {
      title: '状态',
      dataIndex: 'need_renew',
      key: 'need_renew',
      render: (needRenew: boolean) =>
        needRenew ? <Tag color="red">待续费</Tag> : <Tag color="green">正常</Tag>,
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: HourAccount) => (
        <Button
          type="link"
          size="small"
          icon={<RedoOutlined />}
          onClick={() => handleOpenRenew(record)}
        >
          续费
        </Button>
      ),
    },
  ]

  return (
    <div>
      <Title level={3} style={{ marginBottom: 24 }}>
        财务管理
      </Title>

      <Tabs
        items={[
          {
            key: 'accounts',
            label: '课时账户',
            children: (
              <Card>
                <div
                  style={{
                    marginBottom: 16,
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                  }}
                >
                  <Space>
                    <span>仅看待续费</span>
                    <Switch checked={onlyNeedRenew} onChange={setOnlyNeedRenew} />
                    <Tag color="red" style={{ marginInlineStart: 8 }}>
                      剩余 ≤ 5 小时
                    </Tag>
                  </Space>
                  <Button onClick={fetchAccounts}>刷新</Button>
                </div>

                <Table
                  columns={accountColumns}
                  dataSource={accounts}
                  rowKey="id"
                  loading={accountLoading}
                />
              </Card>
            ),
          },
          {
            key: 'payments',
            label: '缴费记录',
            children: (
              <Card>
                <div
                  style={{
                    marginBottom: 16,
                    display: 'flex',
                    justifyContent: 'flex-end',
                  }}
                >
                  <Button type="primary" icon={<PlusOutlined />} onClick={handleCreate}>
                    新增缴费
                  </Button>
                </div>

                <Table
                  columns={columns}
                  dataSource={payments}
                  rowKey="id"
                  loading={loading}
                />
              </Card>
            ),
          },
        ]}
      />

      <Modal
        title={modalType === 'create' ? '新增缴费' : '编辑缴费'}
        open={modalVisible}
        onOk={handleModalSubmit}
        onCancel={() => setModalVisible(false)}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="student_id"
            label="学员"
            rules={[{ required: true, message: '请选择学员' }]}
          >
            <Select placeholder="请选择学员">
              {students.map((s) => (
                <Option key={s.id} value={s.id}>
                  {s.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item name="course_id" label="课程">
            <Select placeholder="请选择课程">
              {courses.map((c) => (
                <Option key={c.id} value={c.id}>
                  {c.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="amount"
            label="金额(元)"
            rules={[{ required: true, message: '请输入金额' }]}
          >
            <InputNumber
              style={{ width: '100%' }}
              min={0}
              precision={2}
              placeholder="请输入金额"
            />
          </Form.Item>
          <Form.Item
            name="payment_method"
            label="支付方式"
            rules={[{ required: true, message: '请选择支付方式' }]}
          >
            <Radio.Group>
              {paymentMethods.map((m) => (
                <Radio key={m.value} value={m.value}>
                  {m.label}
                </Radio>
              ))}
            </Radio.Group>
          </Form.Item>
          <Form.Item
            name="type"
            label="类型"
            rules={[{ required: true, message: '请选择类型' }]}
          >
            <Select placeholder="请选择类型">
              {paymentTypes.map((t) => (
                <Option key={t.value} value={t.value}>
                  {t.label}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="payment_date"
            label="缴费日期"
            rules={[{ required: true, message: '请选择日期' }]}
          >
            <DatePicker style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="remarks" label="备注">
            <TextArea rows={2} placeholder="请输入备注" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="学员续费"
        open={renewVisible}
        onOk={handleRenewSubmit}
        onCancel={() => setRenewVisible(false)}
        confirmLoading={renewSubmitting}
        okText="确认续费"
        cancelText="取消"
        destroyOnClose
      >
        <Form form={renewForm} layout="vertical">
          <Form.Item name="student_id" hidden>
            <Input />
          </Form.Item>
          <Form.Item name="course_id" hidden>
            <Input />
          </Form.Item>

          <Form.Item label="学员">
            <Input value={renewingAccount?.student_name} disabled />
          </Form.Item>
          <Form.Item label="课程">
            <Input value={renewingAccount?.course_name} disabled />
          </Form.Item>
          <Form.Item label="当前剩余课时">
            <Text type={renewingAccount && renewingAccount.remaining_hours <= 5 ? 'danger' : undefined}>
              {renewingAccount?.remaining_hours ?? '-'} 小时
            </Text>
          </Form.Item>

          <Form.Item
            name="hours"
            label="续费课时数(小时)"
            rules={[
              { required: true, message: '请输入续费课时数' },
              {
                validator: (_, value) =>
                  value > 0
                    ? Promise.resolve()
                    : Promise.reject(new Error('课时数必须大于0')),
              },
            ]}
          >
            <InputNumber style={{ width: '100%' }} min={1} precision={0} step={1} />
          </Form.Item>

          <Form.Item
            name="payment_method"
            label="收款方式"
            rules={[{ required: true, message: '请选择收款方式' }]}
          >
            <Radio.Group>
              {paymentMethods.map((m) => (
                <Radio key={m.value} value={m.value}>
                  {m.label}
                </Radio>
              ))}
            </Radio.Group>
          </Form.Item>

          <Form.Item
            name="payment_date"
            label="缴费日期"
            rules={[{ required: true, message: '请选择日期' }]}
          >
            <DatePicker style={{ width: '100%' }} />
          </Form.Item>

          <Form.Item label="应收金额（按课程单价自动计算）">
            <Text strong style={{ fontSize: 16 }}>
              ￥{renewAmount.toFixed(2)}
            </Text>
            <span style={{ color: '#999', marginLeft: 8 }}>
              {renewHours || 0} 小时 × ￥{Number(renewPrice).toFixed(2)}/小时
            </span>
          </Form.Item>

          <Form.Item name="remarks" label="备注">
            <TextArea rows={2} placeholder="请输入备注" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

export default Finance
